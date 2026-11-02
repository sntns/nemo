package credentials

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/valuesource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/cnab"
)

func TestParse(t *testing.T) {
	t.Parallel()

	strategy, err := Parse("token", "path:/tmp/token")
	require.NoError(t, err)
	assert.Equal(t, valuesource.Strategy{Name: "token", Source: valuesource.Source{Key: "path", Value: "/tmp/token"}}, strategy)
	data, err := json.Marshal(strategy)
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"token","source":{"path":"/tmp/token"}}`, string(data), "as a CNAB credential set writes it")

	strategy, err = Parse("token", " command: gh auth token ")
	require.NoError(t, err)
	assert.Equal(t, valuesource.Source{Key: "command", Value: "gh auth token"}, strategy.Source, "the spaces around are not the command's")

	for _, bad := range []string{"/tmp/token", "path:", "path: ", "file:/tmp/token", "vault:secret/x"} {
		_, err := Parse("token", bad)
		assert.Error(t, err, bad)
	}
	assert.Equal(t, "/stack/token", Absolute(valuesource.Strategy{Source: valuesource.Source{Key: "path", Value: "token"}}, "/stack").Source.Value)
	assert.Equal(t, "$HOME/token", Absolute(valuesource.Strategy{Source: valuesource.Source{Key: "path", Value: "$HOME/token"}}, "/stack").Source.Value, "expanded where it is read")
}

func TestResolve(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(path, []byte("s3cret"), 0o600))
	t.Setenv("NEMO_TEST_DB", "postgres://db")

	extendedBundle := cnab.ExtendedBundle{Bundle: bundle.Bundle{Name: "nginx", Credentials: map[string]bundle.Credential{
		"token": {Required: true, ApplyTo: []string{"install", "upgrade"}},
		"db":    {},
	}}}
	token := valuesource.Strategy{Name: "token", Source: valuesource.Source{Key: "path", Value: path}}
	database := valuesource.Strategy{Name: "db", Source: valuesource.Source{Key: "env", Value: "NEMO_TEST_DB"}}

	values, err := Resolve([]valuesource.Strategy{token, database}, extendedBundle, "install")
	require.NoError(t, err)
	assert.Equal(t, valuesource.Set{"token": "s3cret", "db": "postgres://db"}, values)

	missing := valuesource.Strategy{Name: "token", Source: valuesource.Source{Key: "path", Value: "/nowhere"}}
	values, err = Resolve([]valuesource.Strategy{missing}, extendedBundle, "uninstall")
	require.NoError(t, err, "token does not apply to uninstall, so its file is not read")
	assert.Empty(t, values)

	_, err = Resolve(nil, extendedBundle, "install")
	require.EqualError(t, err, "bundle requires credential for token")

	_, err = Resolve([]valuesource.Strategy{token, {Name: "other", Source: valuesource.Source{Key: "env", Value: "X"}}}, extendedBundle, "install")
	require.EqualError(t, err, "credential other is not declared by the bundle nginx")

	merged := Merge(extendedBundle, []valuesource.Strategy{token, {Name: "gone"}}, []valuesource.Strategy{database})
	assert.Equal(t, []valuesource.Strategy{database, token}, merged, "what the bundle still declares, overridden by what is given")
}

func TestWithOverrideAndPack(t *testing.T) {
	t.Parallel()
	definition := cnab.ExtendedBundle{Bundle: bundle.Bundle{
		Name:       "gateway",
		Parameters: map[string]bundle.Parameter{"host": {Destination: &bundle.Location{EnvironmentVariable: "HOST"}}},
		Credentials: map[string]bundle.Credential{
			"certificate":           {Location: bundle.Location{EnvironmentVariable: "CERTIFICATE"}},
			cnab.CredentialOverride: {Location: bundle.Location{EnvironmentVariable: "NEMO_CREDENTIALS"}},
		},
	}}
	override := `
secrets:
  acme-secret: { environment: ACME_SECRET }
  certificate: { environment: CERTIFICATE }
configs:
  host: { environment: HOST }
`
	extended, err := WithOverride(definition, override)
	require.NoError(t, err)
	assert.Equal(t, bundle.Credential{
		Location:    bundle.Location{EnvironmentVariable: "ACME_SECRET"},
		Description: "Read by a secret or a config of the stack's override",
		Required:    true,
		ApplyTo:     []string{cnab.ActionInstall, cnab.ActionUpgrade},
	}, extended.Credentials["acme-secret"], "named after its variable, required as the bundle's own")
	assert.Len(t, extended.Credentials, 3, "a parameter or a credential of the bundle is the bundle's")
	assert.Len(t, definition.Credentials, 2, "the bundle itself is left as it is")

	merged := Merge(extended, []valuesource.Strategy{{Name: "acme-secret", Source: valuesource.Source{Key: "env", Value: "STACK_SECRET"}}}, nil)
	assert.Len(t, merged, 1, "the stack's credential the override reads goes to the installation")

	packed, err := Pack(definition, extended, valuesource.Set{"certificate": "PEM", "acme-secret": "s3cret"})
	require.NoError(t, err)
	assert.Equal(t, "PEM", packed["certificate"])
	assert.JSONEq(t, `{"ACME_SECRET":"s3cret"}`, packed[cnab.CredentialOverride], "by variable, as the installer reads it")
	assert.NotContains(t, packed, "acme-secret")

	unchanged, err := WithOverride(definition, "")
	require.NoError(t, err)
	assert.Equal(t, definition, unchanged)
	packed, err = Pack(definition, definition, valuesource.Set{"certificate": "PEM"})
	require.NoError(t, err)
	assert.Equal(t, valuesource.Set{"certificate": "PEM"}, packed, "nothing to pack without an override")

	older := cnab.ExtendedBundle{Bundle: bundle.Bundle{Name: "gateway"}}
	_, err = Pack(older, extended, valuesource.Set{"acme-secret": "s3cret"})
	require.ErrorContains(t, err, "can't give its override secrets")
}
