package installer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/bundle/definition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/progress"
)

// container is a container a runtime prepared for an install of b.
func container(t *testing.T, bundle bundle.Bundle, environment map[string]string, files fstest.MapFS) Container {
	t.Helper()
	data, err := json.Marshal(bundle)
	require.NoError(t, err)
	if files == nil {
		files = fstest.MapFS{}
	}
	files["cnab/bundle.json"] = &fstest.MapFile{Data: data}

	variables := map[string]string{
		EnvAction:           cnab.ActionInstall,
		EnvInstallationName: "demo/nginx",
		EnvBundleName:       bundle.Name,
		EnvRevision:         "01J9",
	}
	for key, value := range environment {
		variables[key] = value
	}
	return Container{
		LookupEnv: func(key string) (string, bool) { value, ok := variables[key]; return value, ok },
		FS:        files,
	}
}

func testBundle(edit func(cnabBundle *bundle.Bundle)) bundle.Bundle {
	cnabBundle := bundle.Bundle{
		SchemaVersion: "1.2.0",
		Name:          "nginx",
		Version:       "1.0.0",
		InvocationImages: []bundle.InvocationImage{
			{BaseImage: bundle.BaseImage{ImageType: "docker", Image: "gill:dev"}},
		},
		RequiredExtensions: []string{cnab.DockerExtension.Key},
		Custom: map[string]any{
			cnab.DockerExtension.Key: map[string]any{},
			cnab.ComposeExtension.Key: map[string]any{
				"schemaVersion": "1.0.0",
				"compose": []any{map[string]any{"config": map[string]any{
					"services": map[string]any{"web": map[string]any{"image": "nginx:1.27-alpine"}},
				}}},
			},
		},
	}
	if edit != nil {
		edit(&cnabBundle)
	}
	return cnabBundle
}

func TestReadOperation(t *testing.T) {
	t.Parallel()

	cnabBundle := testBundle(func(cnabBundle *bundle.Bundle) {
		cnabBundle.Definitions = definition.Definitions{
			"port":         &definition.Schema{Type: "integer"},
			"orchestrator": &definition.Schema{Type: "string", Default: "swarm"},
			"required":     &definition.Schema{Type: "string"},
		}
		cnabBundle.Parameters = map[string]bundle.Parameter{
			"port":         {Definition: "port", Destination: &bundle.Location{EnvironmentVariable: "PORT"}},
			"orchestrator": {Definition: "orchestrator", Destination: &bundle.Location{EnvironmentVariable: "NEMO_ORCHESTRATOR"}},
			"purge":        {Definition: "required", Required: true, ApplyTo: []string{"uninstall"}, Destination: &bundle.Location{EnvironmentVariable: "PURGE"}},
		}
		cnabBundle.Credentials = map[string]bundle.Credential{
			"token": {Location: bundle.Location{Path: "/cnab/app/credentials/token"}},
		}
	})

	operation, err := ReadOperation(container(t, cnabBundle, map[string]string{"PORT": "8080"}, fstest.MapFS{
		"cnab/app/credentials/token":       {Data: []byte("s3cret")},
		"cnab/app/relocation-mapping.json": {Data: []byte(`{"nginx:1.27":"registry.local/nginx:1.27"}`)},
	}))
	require.NoError(t, err)

	assert.Equal(t, cnab.ActionInstall, operation.Action)
	assert.Equal(t, "demo/nginx", operation.Installation)
	assert.Equal(t, "01J9", operation.Revision)
	assert.Equal(t, "nginx", operation.Bundle.Name)
	assert.NotNil(t, operation.Bundle.Compose)
	assert.Equal(t, map[string]any{"port": 8080, "orchestrator": "swarm"}, operation.Parameters, "typed, defaulted, and only those that apply")
	assert.Equal(t, map[string]string{"token": "s3cret"}, operation.Credentials)
	assert.Equal(t, "registry.local/nginx:1.27", operation.Relocation["nginx:1.27"])
}

func TestReadOperation_errors(t *testing.T) {
	t.Parallel()

	t.Run("no action", func(t *testing.T) {
		t.Parallel()
		_, err := ReadOperation(container(t, testBundle(nil), map[string]string{EnvAction: ""}, nil))
		require.EqualError(t, err, "CNAB_ACTION is not set")
	})

	t.Run("another bundle", func(t *testing.T) {
		t.Parallel()
		_, err := ReadOperation(container(t, testBundle(nil), map[string]string{EnvBundleName: "redis"}, nil))
		require.EqualError(t, err, "CNAB_BUNDLE_NAME is redis, but the bundle is nginx")
	})

	t.Run("missing required parameter", func(t *testing.T) {
		t.Parallel()
		cnabBundle := testBundle(func(cnabBundle *bundle.Bundle) {
			cnabBundle.Definitions = definition.Definitions{"string": &definition.Schema{Type: "string"}}
			cnabBundle.Parameters = map[string]bundle.Parameter{
				"region": {Definition: "string", Required: true, Destination: &bundle.Location{EnvironmentVariable: "REGION"}},
			}
		})
		_, err := ReadOperation(container(t, cnabBundle, nil, nil))
		require.EqualError(t, err, "parameter region is required for install but was not provided")
	})

	t.Run("no relocation", func(t *testing.T) {
		t.Parallel()
		operation, err := ReadOperation(container(t, testBundle(nil), nil, nil))
		require.NoError(t, err)
		assert.Empty(t, operation.Relocation)
	})
}

// fakeOrchestrator is an orchestrator that records what it was asked.
type fakeOrchestrator struct {
	name      string
	installed *Project
	removed   *Installation
	purge     bool
	closed    bool
}

func (fake *fakeOrchestrator) Name() string                    { return fake.name }
func (fake *fakeOrchestrator) Validate(project *Project) error { return nil }
func (fake *fakeOrchestrator) Install(ctx context.Context, project *Project, opts InstallOptions) error {
	fake.installed = project
	return nil
}
func (fake *fakeOrchestrator) Uninstall(ctx context.Context, project *Project, opts UninstallOptions) error {
	fake.removed, fake.purge = &project.Installation, opts.RemoveVolumes
	return nil
}
func (fake *fakeOrchestrator) Close() error {
	fake.closed = true
	return nil
}

// fakeTarget is a set of fake orchestrators, and records which ones were built.
type fakeTarget struct {
	orchestrators map[string]*fakeOrchestrator
	built         []string
}

func newTarget() *fakeTarget {
	return &fakeTarget{orchestrators: map[string]*fakeOrchestrator{"compose": {name: "compose"}, "swarm": {name: "swarm"}}}
}

func (target *fakeTarget) Orchestrators() Orchestrators {
	result := Orchestrators{}
	for name, orchestrator := range target.orchestrators {
		result[name] = func(ctx context.Context) (Orchestrator, error) {
			target.built = append(target.built, name)
			return orchestrator, nil
		}
	}
	return result
}

func TestRun(t *testing.T) {
	t.Parallel()

	read := func(t *testing.T, bundle bundle.Bundle, environment map[string]string) Operation {
		operation, err := ReadOperation(container(t, bundle, environment, nil))
		require.NoError(t, err)
		return operation
	}
	withParameters := func(cnabBundle *bundle.Bundle) {
		cnabBundle.Definitions = definition.Definitions{
			"string":  &definition.Schema{Type: "string"},
			"boolean": &definition.Schema{Type: "boolean", Default: false},
		}
		cnabBundle.Parameters = map[string]bundle.Parameter{
			cnab.ParameterOrchestrator: {Definition: "string", Destination: &bundle.Location{EnvironmentVariable: "NEMO_ORCHESTRATOR"}},
			cnab.ParameterPurge:        {Definition: "boolean", ApplyTo: []string{"uninstall"}, Destination: &bundle.Location{EnvironmentVariable: "NEMO_PURGE"}},
		}
	}

	t.Run("builds only the orchestrator the parameter names", func(t *testing.T) {
		t.Parallel()
		target := newTarget()
		operation := read(t, testBundle(withParameters), map[string]string{"NEMO_ORCHESTRATOR": "compose"})
		require.NoError(t, Run(context.Background(), operation, target.Orchestrators(), progress.Discard))
		assert.Equal(t, []string{"compose"}, target.built)
		compose := target.orchestrators["compose"]
		require.NotNil(t, compose.installed)
		assert.Equal(t, Installation{Stack: "demo", Bundle: "nginx", Version: "1.0.0", Revision: "01J9"}, compose.installed.Installation)
		assert.True(t, compose.closed)
	})

	t.Run("upgrade applies the project", func(t *testing.T) {
		t.Parallel()
		target := newTarget()
		operation := read(t, testBundle(withParameters), map[string]string{EnvAction: cnab.ActionUpgrade, "NEMO_ORCHESTRATOR": "swarm"})
		require.NoError(t, Run(context.Background(), operation, target.Orchestrators(), progress.Discard))
		assert.NotNil(t, target.orchestrators["swarm"].installed)
	})

	t.Run("validate only checks", func(t *testing.T) {
		t.Parallel()
		target := newTarget()
		operation := read(t, testBundle(withParameters), map[string]string{EnvAction: cnab.ActionValidate, "NEMO_ORCHESTRATOR": "swarm"})
		require.NoError(t, Run(context.Background(), operation, target.Orchestrators(), progress.Discard))
		assert.Nil(t, target.orchestrators["swarm"].installed)
	})

	t.Run("never guesses the orchestrator", func(t *testing.T) {
		t.Parallel()
		target := newTarget()
		err := Run(context.Background(), read(t, testBundle(withParameters), nil), target.Orchestrators(), progress.Discard)
		require.ErrorContains(t, err, "parameter nemo-orchestrator is not set")
		assert.Empty(t, target.built)
	})

	t.Run("unknown orchestrator", func(t *testing.T) {
		t.Parallel()
		operation := read(t, testBundle(withParameters), map[string]string{"NEMO_ORCHESTRATOR": "kubernetes"})
		require.EqualError(t, Run(context.Background(), operation, newTarget().Orchestrators(), progress.Discard), "orchestrator kubernetes: unsupported")
	})

	t.Run("orchestrator that can't connect", func(t *testing.T) {
		t.Parallel()
		failing := Orchestrators{"swarm": func(ctx context.Context) (Orchestrator, error) { return nil, errors.New("not a swarm manager") }}
		operation := read(t, testBundle(withParameters), map[string]string{"NEMO_ORCHESTRATOR": "swarm"})
		require.EqualError(t, Run(context.Background(), operation, failing, progress.Discard), "orchestrator swarm: not a swarm manager")
	})

	t.Run("uninstall with purge", func(t *testing.T) {
		t.Parallel()
		target := newTarget()
		operation := read(t, testBundle(withParameters), map[string]string{EnvAction: cnab.ActionUninstall, "NEMO_ORCHESTRATOR": "swarm", "NEMO_PURGE": "true"})
		require.NoError(t, Run(context.Background(), operation, target.Orchestrators(), progress.Discard))
		swarm := target.orchestrators["swarm"]
		require.NotNil(t, swarm.removed)
		assert.Equal(t, "demo", swarm.removed.Stack)
		assert.True(t, swarm.purge)
	})

	t.Run("undeclared action", func(t *testing.T) {
		t.Parallel()
		operation := read(t, testBundle(withParameters), map[string]string{EnvAction: "logs", "NEMO_ORCHESTRATOR": "compose"})
		require.EqualError(t, Run(context.Background(), operation, newTarget().Orchestrators(), progress.Discard), "action logs: unsupported")
	})

	t.Run("no compose", func(t *testing.T) {
		t.Parallel()
		cnabBundle := testBundle(func(bundle *bundle.Bundle) {
			withParameters(bundle)
			delete(bundle.Custom, cnab.ComposeExtension.Key)
		})
		operation := read(t, cnabBundle, map[string]string{"NEMO_ORCHESTRATOR": "compose"})
		require.ErrorContains(t, Run(context.Background(), operation, newTarget().Orchestrators(), progress.Discard), "no io.nemo.compose extension")
	})
}

func TestRegistryAuth(t *testing.T) {
	t.Parallel()
	auth, err := registryAuthOf(map[string]any{cnab.ParameterRegistryAuth: `{"ghcr.io":{"username":"bot","password":"s3cret"}}`})
	require.NoError(t, err)

	encoded, err := auth.For("ghcr.io/sntns/web@sha256:" + strings.Repeat("a", 64))
	require.NoError(t, err)
	assert.NotEmpty(t, encoded)
	encoded, err = auth.For("nginx:1.27-alpine")
	require.NoError(t, err)
	assert.Empty(t, encoded, "Docker Hub has no credentials here")

	_, err = registryAuthOf(map[string]any{cnab.ParameterRegistryAuth: `{"ghcr.io":"s3cret"}`})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s3cret", "the value never shows")
}
