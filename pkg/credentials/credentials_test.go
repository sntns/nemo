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
