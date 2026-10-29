package stack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/valuesource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
)

func TestLoad(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "stack.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: demo
target:
  orchestrator: swarm
  configuration:
    host: tcp://vm1:2376
    tls:
      ca: { path: certs/ca.pem }
      cert: { path: certs/cert.pem }
      key: { env: DOCKER_KEY }
    volumes:
      data:
        driver: local
        provision: { directory: "{{.Name}}", volume: { driver: local } }
parameters: { region: eu5 }
credentials:
  - { name: tls, source: { path: certs/tls.pem } }
installations:
  - name: nginx
    bundle: { file: nginx/bundle.json }
    credentials:
      - { name: token, source: { path: token } }
      - { name: other, source: { env: TOKEN } }
`), 0o600))

	stack, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "demo", stack.Name)
	assert.Equal(t, target.Swarm, stack.Target.Orchestrator)
	assert.Equal(t, "local", stack.Target.Volumes["data"].Driver, "the target's JSON names")
	assert.Equal(t, filepath.Join(directory, "certs/ca.pem"), stack.Target.TLS.CA.Value, "the target's TLS, relative to the stack file")
	assert.Equal(t, valuesource.Source{Key: "env", Value: "DOCKER_KEY"}, *stack.Target.TLS.Key)
	assert.Equal(t, filepath.Join(directory, "certs/tls.pem"), stack.Credentials[0].Source.Value, "the stack's credentials, relative to the stack file")
	nginx := stack.Installations[0]
	assert.Equal(t, "demo", nginx.Stack, "the stack's installations are in it")
	assert.Equal(t, filepath.Join(directory, "nginx/bundle.json"), nginx.Bundle.File, "relative to the stack file")
	assert.Equal(t, filepath.Join(directory, "token"), nginx.Credentials[0].Source.Value, "a path relative to the stack file")
	assert.Equal(t, valuesource.Source{Key: "env", Value: "TOKEN"}, nginx.Credentials[1].Source)

	require.NoError(t, os.WriteFile(path, []byte("name: demo\ntarget: { orchestrator: compose }\ninstallations: [{ name: x, bundle: { fil: x } }]\n"), 0o600))
	_, err = Load(path)
	require.ErrorContains(t, err, `unknown field "fil"`)
}

func TestLoad_references(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "stack.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: demo
target: { orchestrator: swarm }
remote: "oci:.artifacts/prod/{{ .Component }}.oci"
installations:
  - { name: sntns-cloud-service-faas, bundle: { version: 1.4.0 } }
  - { name: whoami, bundle: { repository: localhost:5001/whoami, version: 1.0.0 } }
`), 0o600))

	stack, err := Load(path)
	require.NoError(t, err)
	source, err := stack.Source(stack.Installations[0])
	require.NoError(t, err)
	assert.Equal(t, "oci:"+filepath.Join(directory, ".artifacts/prod/sntns-cloud-service-faas.oci"), source, "the remote, relative to the stack file")
	source, err = stack.Source(stack.Installations[1])
	require.NoError(t, err)
	assert.Equal(t, "docker://localhost:5001/whoami:1.0.0", source, "its repository, as Porter's")

	stack.Remote = "docker://ghcr.io/sntns/{{ .Component }}:{{ .Version }}"
	source, err = stack.Source(stack.Installations[0])
	require.NoError(t, err)
	assert.Equal(t, "docker://ghcr.io/sntns/sntns-cloud-service-faas:1.4.0", source)

	for installations, message := range map[string]string{
		"[{ name: faas, bundle: {} }]":                                   "installations: (0: (bundle: (version: cannot be blank.).).)",
		"[{ bundle: { version: 1.0.0 } }]":                               "installations: (0: (name: cannot be blank.).)",
		"[{ name: faas, bundle: { version: 1.0.0, file: x.json } }]":     "installations: (0: (bundle: (version: a file has its own version.).).)",
		"[{ name: faas, bundle: { repository: x/faas, file: x.json } }]": "installations: (0: (bundle: (repository: give a file or a repository, not both.).).)",
		"[{ name: faas, stack: other, bundle: { file: x.json } }]":       "installations: (0: faas is in the stack other.)",
	} {
		require.NoError(t, os.WriteFile(path, []byte("name: demo\ntarget: { orchestrator: swarm }\nremote: docker://x/{{ .Component }}\ninstallations: "+installations+"\n"), 0o600))
		_, err := Load(path)
		require.ErrorContains(t, err, message, installations)
	}
	require.NoError(t, os.WriteFile(path, []byte("name: demo\ntarget: { orchestrator: swarm }\ninstallations: [{ name: faas, bundle: { version: 1.0.0 } }]\n"), 0o600))
	_, err = Load(path)
	require.ErrorContains(t, err, "stack demo: remote: where are faas? Give it in the manifest, or with nemo stack import --remote")
	installations := []storage.InstallationSpec{{Name: "x", Bundle: storage.BundleReference{File: "x"}}}
	require.ErrorContains(t, Stack{Name: "a/b", Target: target.Target{Orchestrator: target.Swarm}, Installations: installations}.Validate(), `name: must be a name without / or \.`)
}

func testBundle(t *testing.T, name, version string, dependencies map[string]cnab.Dependency) cnab.ExtendedBundle {
	t.Helper()
	extendedBundle := cnab.ExtendedBundle{Bundle: bundle.Bundle{Name: name, Version: version}}
	if dependencies != nil {
		extendedBundle.Dependencies = &cnab.Dependencies{Requires: dependencies}
	}
	return extendedBundle
}

func TestOrder(t *testing.T) {
	t.Parallel()
	onDatabase := map[string]cnab.Dependency{"db": {Bundle: "registry.example.com/bundles/database:1.0.0"}}
	bundles := []cnab.ExtendedBundle{
		testBundle(t, "web", "1.0.0", onDatabase),
		testBundle(t, "cache", "1.0.0", map[string]cnab.Dependency{"outside": {Bundle: "registry.example.com/bundles/outside"}}),
		testBundle(t, "database", "1.0.0", nil),
	}
	ordered, err := Order(bundles)
	require.NoError(t, err)
	var names []string
	for _, bundle := range ordered {
		names = append(names, bundle.Name)
	}
	assert.Equal(t, []string{"cache", "database", "web"}, names, "a dependency outside the set orders nothing")

	_, err = Order([]cnab.ExtendedBundle{
		testBundle(t, "database", "1.0.0", map[string]cnab.Dependency{"web": {Bundle: "web"}}),
		testBundle(t, "web", "1.0.0", onDatabase),
	})
	require.EqualError(t, err, "the dependencies of database, web are a cycle")
}

func TestCheckDependencies(t *testing.T) {
	t.Parallel()
	ranged := map[string]cnab.Dependency{"db": {Bundle: "registry.example.com/bundles/database", Version: &cnab.DependencyVersion{Ranges: []string{"^2.0.0"}}}}

	require.NoError(t, CheckDependencies([]cnab.ExtendedBundle{testBundle(t, "web", "1.0.0", ranged), testBundle(t, "database", "2.1.0", nil)}))

	err := CheckDependencies([]cnab.ExtendedBundle{testBundle(t, "web", "1.0.0", ranged), testBundle(t, "database", "1.4.0", nil)})
	require.EqualError(t, err, "web depends on registry.example.com/bundles/database: version 1.4.0 is in none of ^2.0.0")

	either := map[string]cnab.Dependency{"db": {Bundle: "registry.example.com/bundles/database", Version: &cnab.DependencyVersion{Ranges: []string{"^1.0.0", "^2.0.0"}}}}
	require.NoError(t, CheckDependencies([]cnab.ExtendedBundle{testBundle(t, "web", "1.0.0", either), testBundle(t, "database", "1.4.0", nil)}), "any of the ranges, as the specification reads them")

	err = CheckDependencies([]cnab.ExtendedBundle{testBundle(t, "web", "1.0.0", ranged)})
	require.EqualError(t, err, "web depends on registry.example.com/bundles/database, which the stack does not have")
	assert.Len(t, Missing([]cnab.ExtendedBundle{testBundle(t, "web", "1.0.0", ranged)}), 1)

	err = CheckDependencies([]cnab.ExtendedBundle{testBundle(t, "web", "1.0.0", map[string]cnab.Dependency{"db": {Bundle: "Not A Reference"}})})
	require.ErrorContains(t, err, "web: dependency")
}
