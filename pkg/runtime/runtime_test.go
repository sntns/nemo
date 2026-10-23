package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/bundle/definition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/target"
)

func testBundle(t *testing.T) cnab.ExtendedBundle {
	t.Helper()
	writeOnly := true
	extendedBundle, err := cnab.NewBundle(bundle.Bundle{
		SchemaVersion: "1.2.0",
		Name:          "nginx",
		Version:       "1.0.0",
		InvocationImages: []bundle.InvocationImage{
			{BaseImage: bundle.BaseImage{ImageType: "docker", Image: "gill:dev"}},
		},
		RequiredExtensions: []string{cnab.DockerExtension.Key},
		Custom:             map[string]any{cnab.DockerExtension.Key: map[string]any{}},
		Definitions: definition.Definitions{
			"orchestrator": &definition.Schema{Type: "string", Enum: []any{"compose", "swarm"}},
			"port":         &definition.Schema{Type: "integer", Default: 8080},
			"region":       &definition.Schema{Type: "string"},
			"boolean":      &definition.Schema{Type: "boolean", Default: false},
			"volumes":      &definition.Schema{Type: "object"},
			"secret":       &definition.Schema{Type: "string", WriteOnly: &writeOnly},
		},
		Parameters: map[string]bundle.Parameter{
			cnab.ParameterOrchestrator: {Definition: "orchestrator", Required: true, Destination: &bundle.Location{EnvironmentVariable: "NEMO_ORCHESTRATOR"}},
			"port":                     {Definition: "port", Destination: &bundle.Location{EnvironmentVariable: "PORT"}},
			"region":                   {Definition: "region", Required: true, ApplyTo: []string{"install", "upgrade"}, Destination: &bundle.Location{EnvironmentVariable: "REGION"}},
			cnab.ParameterPurge:        {Definition: "boolean", ApplyTo: []string{"uninstall"}, Destination: &bundle.Location{EnvironmentVariable: "NEMO_PURGE"}},
			cnab.ParameterVolumes:      {Definition: "volumes", Destination: &bundle.Location{EnvironmentVariable: "NEMO_VOLUMES"}},
			cnab.ParameterRegistryAuth: {Definition: "secret", ApplyTo: []string{"install", "upgrade"}, Destination: &bundle.Location{EnvironmentVariable: "NEMO_REGISTRY_AUTH"}},
		},
	})
	require.NoError(t, err)
	return extendedBundle
}

func TestParametersOf(t *testing.T) {
	t.Parallel()

	newRequest := func(action string, given map[string]string) Request {
		return Request{Bundle: testBundle(t), Stack: "demo", Action: action, Target: target.Target{Orchestrator: target.Compose}, Parameters: given}
	}

	t.Run("converted, defaulted, orchestrator from the target", func(t *testing.T) {
		t.Parallel()
		values, err := parametersOf(newRequest("install", map[string]string{"region": "eu5", "port": "9090"}), "")
		require.NoError(t, err)
		assert.Equal(t, map[string]any{cnab.ParameterOrchestrator: "compose", "port": 9090, "region": "eu5"}, values)
	})

	t.Run("only those that apply to the action", func(t *testing.T) {
		t.Parallel()
		values, err := parametersOf(newRequest("uninstall", nil), "")
		require.NoError(t, err)
		assert.Equal(t, map[string]any{cnab.ParameterOrchestrator: "compose", "port": 8080, cnab.ParameterPurge: false}, values)
	})

	t.Run("the orchestrator can't be given", func(t *testing.T) {
		t.Parallel()
		_, err := parametersOf(newRequest("install", map[string]string{"region": "eu5", cnab.ParameterOrchestrator: "swarm"}), "")
		require.EqualError(t, err, "parameter nemo-orchestrator comes from the target, it can't be given")
	})

	t.Run("volume types from the target", func(t *testing.T) {
		t.Parallel()
		request := newRequest("install", map[string]string{"region": "eu5"})
		request.Target.Volumes = map[string]target.VolumeType{"data": {VolumeDriver: target.VolumeDriver{Driver: "local"}}}
		values, err := parametersOf(request, "")
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"data": map[string]any{"driver": "local"}}, values[cnab.ParameterVolumes])

		_, err = parametersOf(newRequest("install", map[string]string{"region": "eu5", cnab.ParameterVolumes: "{}"}), "")
		require.EqualError(t, err, "parameter nemo-volumes comes from the target, it can't be given")
	})

	t.Run("registry credentials from the runtime", func(t *testing.T) {
		t.Parallel()
		values, err := parametersOf(newRequest("install", map[string]string{"region": "eu5"}), `{"ghcr.io":{"username":"bot"}}`)
		require.NoError(t, err)
		assert.Equal(t, `{"ghcr.io":{"username":"bot"}}`, values[cnab.ParameterRegistryAuth])

		_, err = parametersOf(newRequest("install", map[string]string{"region": "eu5", cnab.ParameterRegistryAuth: "{}"}), "")
		require.EqualError(t, err, "parameter nemo-registry-auth comes from docker login, it can't be given")
	})

	t.Run("checked against the definitions", func(t *testing.T) {
		t.Parallel()
		request := newRequest("install", map[string]string{"region": "eu5", "other": "x"})
		request.Target.Orchestrator = "kubernetes"
		_, err := parametersOf(request, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parameter other is not declared by the bundle nginx")
		assert.Contains(t, err.Error(), "parameter nemo-orchestrator:")
	})

	t.Run("required", func(t *testing.T) {
		t.Parallel()
		_, err := parametersOf(newRequest("install", nil), "")
		require.EqualError(t, err, "parameter region is required for install")
	})
}

func TestNewClaim(t *testing.T) {
	t.Parallel()

	claim, err := newClaim("demo/nginx", "install", testBundle(t), map[string]any{"region": "eu5"})
	require.NoError(t, err, "the / of <stack>/<bundle> is accepted")
	assert.Equal(t, "demo/nginx", claim.Installation)
	assert.NotEmpty(t, claim.ID)
	assert.NotEmpty(t, claim.Revision)
	assert.NotEqual(t, claim.ID, claim.Revision)

	_, err = newClaim("demo/nginx", "logs", testBundle(t), nil)
	require.Error(t, err, "an undeclared action")
}

func TestNewDriver(t *testing.T) {
	t.Parallel()

	unreachable := target.Target{Orchestrator: target.Compose, Configuration: target.Configuration{Host: "tcp://127.0.0.1:1"}}
	_, err := newDriver(context.Background(), unreachable, target.Installer{}, testBundle(t))
	require.ErrorContains(t, err, "reaching the target's Docker at tcp://127.0.0.1:1")

	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(kubeconfig, []byte(`
apiVersion: v1
kind: Config
clusters: [{name: test, cluster: {server: "https://127.0.0.1:1"}}]
contexts: [{name: test, context: {cluster: test, user: test}}]
current-context: test
users: [{name: test, user: {token: x}}]
`), 0o600))
	kubernetes := target.Installer{Driver: target.DriverKubernetes, Kubernetes: &target.Kubernetes{Namespace: "nemo", Kubeconfig: kubeconfig}}
	_, err = newDriver(context.Background(), unreachable, kubernetes, testBundle(t))
	require.NoError(t, err, "nothing is reached before the action runs")
	_, err = newDriver(context.Background(), target.Target{Orchestrator: target.Compose, Configuration: target.Configuration{Host: "unix:///var/run/docker.sock"}}, kubernetes, testBundle(t))
	require.ErrorContains(t, err, "reaches the target's Docker over tcp:// only")
}
