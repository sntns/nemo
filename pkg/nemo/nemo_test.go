package nemo

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/bundle/definition"
	"github.com/cnabio/cnab-go/valuesource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/runtime"
	"github.com/sntns/nemo/pkg/stack"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/storage/file"
	"github.com/sntns/nemo/pkg/target"
)

// fakeRuntime records the requests and answers with a successful run.
type fakeRuntime struct {
	requests []runtime.Request
	// failing names the bundles whose actions fail, as the installer's would.
	failing map[string]bool
	// outputs are those of every action.
	outputs map[string]string
}

func (fake *fakeRuntime) execute(ctx context.Context, request runtime.Request) (runtime.Run, error) {
	fake.requests = append(fake.requests, request)
	parameters := map[string]any{}
	for name, value := range request.Parameters {
		parameters[name] = value
	}
	parameters[cnab.ParameterOrchestrator] = request.Target.Orchestrator
	created := cnab.Claim{
		ID:           cnab.NewULID(),
		Revision:     cnab.NewULID(),
		Installation: request.Stack + "/" + request.Bundle.Name,
		Action:       request.Action,
		Bundle:       request.Bundle.Bundle,
		Parameters:   parameters,
		// As the runtime records them.
		BundleReference: request.Source,
		Custom:          request.Custom,
	}
	if fake.failing[request.Bundle.Name] {
		result, err := created.NewResult(cnab.StatusFailed)
		if err != nil {
			return runtime.Run{}, err
		}
		return runtime.Run{Claim: created, Result: result}, &runtime.FailedError{Action: request.Action, Installation: created.Installation, Cause: "the installer exited with code 1"}
	}
	result, err := created.NewResult(cnab.StatusSucceeded)
	return runtime.Run{Claim: created, Result: result, Outputs: maps.Clone(fake.outputs)}, err
}

func (fake *fakeRuntime) last() runtime.Request { return fake.requests[len(fake.requests)-1] }

func setup(t *testing.T) (*Nemo, *fakeRuntime, string, string) {
	t.Helper()
	directory := t.TempDir()
	home := filepath.Join(directory, "home")
	token := filepath.Join(directory, "token")
	require.NoError(t, os.WriteFile(token, []byte("s3cret"), 0o600))

	sensitive := true
	cnabBundle := bundle.Bundle{
		SchemaVersion: "1.2.0",
		Name:          "nginx",
		Version:       "1.0.0",
		InvocationImages: []bundle.InvocationImage{
			{BaseImage: bundle.BaseImage{ImageType: "docker", Image: "gill:dev"}},
		},
		RequiredExtensions: []string{cnab.DockerExtension.Key},
		Custom:             map[string]any{cnab.DockerExtension.Key: map[string]any{}},
		Definitions: definition.Definitions{
			"string":   &definition.Schema{Type: "string"},
			"password": &definition.Schema{Type: "string", WriteOnly: &sensitive},
		},
		Parameters: map[string]bundle.Parameter{
			cnab.ParameterOrchestrator: {Definition: "string", Destination: &bundle.Location{EnvironmentVariable: "NEMO_ORCHESTRATOR"}},
			"region":                   {Definition: "string", Destination: &bundle.Location{EnvironmentVariable: "REGION"}},
			"password":                 {Definition: "password", Destination: &bundle.Location{EnvironmentVariable: "PASSWORD"}},
		},
		Credentials: map[string]bundle.Credential{
			"token": {Location: bundle.Location{Path: "/cnab/app/credentials/token"}, Required: true, ApplyTo: []string{"install", "upgrade"}},
		},
	}
	data, err := json.Marshal(cnabBundle)
	require.NoError(t, err)
	bundleFile := filepath.Join(directory, "bundle.json")
	require.NoError(t, os.WriteFile(bundleFile, data, 0o600))

	fake := &fakeRuntime{}
	return &Nemo{store: file.New(home), execute: fake.execute, load: LoadBundle}, fake, bundleFile, token
}

// demoNginx names the installation of the test bundle in the stack demo.
var demoNginx = storage.InstallationSpec{Name: "nginx", Stack: "demo"}

func TestInstallThenUninstallWithNothingElse(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, token := setup(t)
	ctx := context.Background()

	_, err := nemo.Install(ctx, ActionRequest{
		InstallationSpec: storage.InstallationSpec{
			Stack:       "demo",
			Parameters:  map[string]string{"region": "eu5", "password": "hunter2"},
			Credentials: []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "path", Value: token}}},
		},
		BundleSource: bundleFile,
		Target:       target.Target{Orchestrator: target.Swarm},
	})
	require.NoError(t, err)

	run, err := nemo.Uninstall(ctx, ActionRequest{InstallationSpec: demoNginx})
	require.NoError(t, err)
	assert.Equal(t, "uninstall", run.Action)

	request := fake.last()
	assert.Equal(t, "nginx", request.Bundle.Name, "the stored bundle")
	assert.Equal(t, target.Swarm, request.Target.Orchestrator, "the stored target")
	assert.Equal(t, map[string]string{"region": "eu5"}, request.Parameters, "stored parameters, the sensitive one not")
	assert.Empty(t, request.Credentials, "token does not apply to uninstall")
}

// cancelling refuses a context that ended, as the store of a target does:
// it writes through Docker.
type cancelling struct{ storage.Store }

func (store cancelling) SaveRun(ctx context.Context, stack, name string, run cnab.Claim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return store.Store.SaveRun(ctx, stack, name, run)
}

// An action whose context ends while it runs, at Terraform's timeout or at
// ^C, is recorded all the same: it failed.
func TestAnActionCutShortIsRecorded(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, token := setup(t)
	nemo.store = cancelling{nemo.store}
	fake.failing = map[string]bool{"nginx": true}
	ctx, cancel := context.WithCancel(context.Background())
	nemo.execute = func(ctx context.Context, request runtime.Request) (runtime.Run, error) {
		cancel()
		return fake.execute(ctx, request)
	}

	_, err := nemo.Install(ctx, ActionRequest{
		InstallationSpec: storage.InstallationSpec{
			Stack:       "demo",
			Credentials: []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "path", Value: token}}},
		},
		BundleSource: bundleFile,
		Target:       target.Target{Orchestrator: target.Swarm},
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "could not be recorded")
	installation, err := nemo.Installation(context.Background(), "demo", "nginx")
	require.NoError(t, err)
	assert.Equal(t, cnab.StatusFailed, installation.Status.ResultStatus)
}

func TestUpgradeOverridesParameters(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, token := setup(t)
	ctx := context.Background()

	_, err := nemo.Install(ctx, ActionRequest{
		InstallationSpec: storage.InstallationSpec{Stack: "demo", Parameters: map[string]string{"region": "eu5"}, Credentials: []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "path", Value: token}}}},
		BundleSource:     bundleFile,
		Target:           target.Target{Orchestrator: target.Compose},
	})
	require.NoError(t, err)

	_, err = nemo.Upgrade(ctx, ActionRequest{InstallationSpec: storage.InstallationSpec{Name: "nginx", Stack: "demo", Parameters: map[string]string{"region": "eu6"}}})
	require.NoError(t, err)
	request := fake.last()
	assert.Equal(t, map[string]string{"region": "eu6"}, request.Parameters)
	assert.Equal(t, valuesource.Set{"token": "s3cret"}, request.Credentials, "read again from the stored source")
}

func TestRefusals(t *testing.T) {
	t.Parallel()
	nemo, _, bundleFile, token := setup(t)
	ctx := context.Background()

	_, err := nemo.Upgrade(ctx, ActionRequest{InstallationSpec: demoNginx})
	require.EqualError(t, err, "demo/nginx is not installed")
	_, err = nemo.Uninstall(ctx, ActionRequest{InstallationSpec: demoNginx})
	require.EqualError(t, err, "nemo does not know demo/nginx: give its bundle's source and its target")
	_, err = nemo.Install(ctx, ActionRequest{InstallationSpec: demoNginx})
	require.EqualError(t, err, "install needs the bundle's source")

	_, err = nemo.Install(ctx, ActionRequest{
		InstallationSpec: storage.InstallationSpec{Stack: "demo", Credentials: []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "path", Value: token}}}},
		BundleSource:     bundleFile,
		Target:           target.Target{Orchestrator: target.Compose},
	})
	require.NoError(t, err)
	_, err = nemo.Upgrade(ctx, ActionRequest{InstallationSpec: demoNginx, Target: target.Target{Orchestrator: target.Swarm}})
	require.EqualError(t, err, "demo/nginx is deployed with compose: uninstall it with compose first")
	_, err = nemo.Upgrade(ctx, ActionRequest{InstallationSpec: demoNginx, Target: target.Target{Configuration: target.Configuration{Host: "ssh://vm2"}}})
	require.EqualError(t, err, "demo/nginx is deployed on the environment's Docker: uninstall it there first")

	_, err = nemo.Uninstall(ctx, ActionRequest{InstallationSpec: demoNginx})
	require.NoError(t, err)
	_, err = nemo.Upgrade(ctx, ActionRequest{InstallationSpec: demoNginx})
	require.EqualError(t, err, "demo/nginx is not installed")
}

func TestNothingSecretIsWritten(t *testing.T) {
	t.Parallel()
	nemo, _, bundleFile, token := setup(t)

	run, err := nemo.Install(context.Background(), ActionRequest{
		InstallationSpec: storage.InstallationSpec{Stack: "demo", Parameters: map[string]string{"password": "hunter2"}, Credentials: []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "path", Value: token}}}},
		BundleSource:     bundleFile,
		Target:           target.Target{Orchestrator: target.Compose},
	})
	require.NoError(t, err)
	assert.Equal(t, Redacted, run.Parameters["password"], "nor returned")

	home := filepath.Join(filepath.Dir(bundleFile), "home")
	err = filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, secret := range []string{"hunter2", "s3cret"} {
			assert.False(t, strings.Contains(string(data), secret), "%s holds %s", path, secret)
		}
		return nil
	})
	require.NoError(t, err)
}

// A credential, or a part of the target's TLS, given by value is recorded
// without its value: an action that needs it is given it again, and one
// that does not, an uninstall, goes on without it.
func TestAValueIsNotRecorded(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	ctx := context.Background()
	byValue := []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "value", Value: "v4lue"}}}
	ca := "-----BEGIN CERTIFICATE-----"
	deployment := target.Target{Orchestrator: target.Compose, Configuration: target.Configuration{
		Host: "tcp://10.1.0.10:2376", TLS: &target.TLS{CA: &valuesource.Source{Key: "value", Value: ca}},
	}}

	run, err := nemo.Install(ctx, ActionRequest{InstallationSpec: storage.InstallationSpec{Stack: "demo", Credentials: byValue}, BundleSource: bundleFile, Target: deployment})
	require.NoError(t, err)
	assert.Equal(t, valuesource.Set{"token": "v4lue"}, fake.last().Credentials, "the action has it")
	assert.Equal(t, ca, fake.last().Target.TLS.CA.Value, "and reaches the target with it")
	assert.Equal(t, Redacted, run.Target.TLS.CA.Value, "not returned")
	err = filepath.Walk(filepath.Join(filepath.Dir(bundleFile), "home"), func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, secret := range []string{"v4lue", ca} {
			assert.False(t, strings.Contains(string(data), secret), "%s holds %s", path, secret)
		}
		return nil
	})
	require.NoError(t, err)
	installation, err := nemo.Installation(ctx, "demo", "nginx")
	require.NoError(t, err)
	assert.Equal(t, []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "value", Value: Redacted}}}, installation.Credentials, "its source kept")

	_, err = nemo.Upgrade(ctx, ActionRequest{InstallationSpec: demoNginx})
	require.ErrorContains(t, err, "credential token was given by value, which nemo does not record: give it again")
	_, err = nemo.Upgrade(ctx, ActionRequest{InstallationSpec: storage.InstallationSpec{Name: "nginx", Stack: "demo", Credentials: byValue}})
	require.NoError(t, err, "given again")
	_, err = nemo.Uninstall(ctx, ActionRequest{InstallationSpec: demoNginx})
	assert.NoError(t, err, "token applies to install and upgrade only")
}

// writeSourcedBundle writes a bundle that reads its state from the output
// of its last run.
func writeSourcedBundle(t *testing.T, directory string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"schemaVersion":      "1.2.0",
		"name":               "stateful",
		"version":            "0.1.0",
		"invocationImages":   []any{map[string]any{"imageType": "docker", "image": "registry.example.com/stateful:0.1.0"}},
		"requiredExtensions": []any{"io.cnab.parameter-sources"},
		"custom": map[string]any{
			"io.cnab.parameter-sources": map[string]any{
				"state": map[string]any{"priority": []any{"output"}, "sources": map[string]any{"output": map[string]any{"name": "state"}}},
			},
		},
		"definitions": map[string]any{"string": map[string]any{"type": "string"}},
		"parameters":  map[string]any{"state": map[string]any{"definition": "string", "destination": map[string]any{"path": "/cnab/app/state"}}},
		"outputs":     map[string]any{"state": map[string]any{"definition": "string", "path": "/cnab/app/outputs/state"}},
	})
	require.NoError(t, err)
	path := filepath.Join(directory, "stateful.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// A parameter the bundle reads from an output gets the output of the
// installation's last run that has it, and is never recorded with the
// installation's parameters.
func TestSourcedParameters(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	ctx := context.Background()
	stateful := storage.InstallationSpec{Name: "stateful", Stack: "demo"}

	fake.outputs = map[string]string{"state": "run 1"}
	_, err := nemo.Install(ctx, ActionRequest{InstallationSpec: storage.InstallationSpec{Stack: "demo"}, BundleSource: writeSourcedBundle(t, filepath.Dir(bundleFile)), Target: target.Target{Orchestrator: target.Compose}})
	require.NoError(t, err)
	assert.NotContains(t, fake.last().Parameters, "state", "no run before, no state")

	fake.outputs = map[string]string{"state": "run 2"}
	_, err = nemo.Upgrade(ctx, ActionRequest{InstallationSpec: stateful})
	require.NoError(t, err)
	assert.Equal(t, "run 1", fake.last().Parameters["state"], "the install's output")

	fake.outputs = nil
	_, err = nemo.Uninstall(ctx, ActionRequest{InstallationSpec: stateful})
	require.NoError(t, err)
	assert.Equal(t, "run 2", fake.last().Parameters["state"], "the latest")

	installation, err := nemo.Installation(ctx, "demo", "stateful")
	require.NoError(t, err)
	assert.NotContains(t, installation.Parameters, "state", "read again at each action")
}

func TestInstallationStatus(t *testing.T) {
	t.Parallel()
	nemo, _, bundleFile, token := setup(t)
	ctx := context.Background()
	install := ActionRequest{
		InstallationSpec: storage.InstallationSpec{Stack: "demo", Credentials: []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "path", Value: token}}}},
		BundleSource:     bundleFile,
		Target:           target.Target{Orchestrator: target.Compose},
	}

	_, err := nemo.Install(ctx, install)
	require.NoError(t, err)
	installation, err := nemo.store.GetInstallation(ctx, "demo", "nginx")
	require.NoError(t, err)
	assert.True(t, installation.IsInstalled())
	assert.Equal(t, cnab.ActionInstall, installation.Status.Action)
	assert.Equal(t, bundleFile, installation.Status.BundleReference)
	assert.Equal(t, "1.0.0", installation.Status.BundleVersion)
	assert.Equal(t, storage.BundleReference{File: bundleFile}, installation.Bundle, "the reference of the bundle.json it was given")
	firstInstall := *installation.Status.Installed

	_, err = nemo.Uninstall(ctx, ActionRequest{InstallationSpec: demoNginx})
	require.NoError(t, err)
	installation, err = nemo.store.GetInstallation(ctx, "demo", "nginx")
	require.NoError(t, err)
	assert.False(t, installation.IsInstalled())
	assert.NotNil(t, installation.Status.Uninstalled)

	_, err = nemo.Install(ctx, install)
	require.NoError(t, err)
	installation, err = nemo.store.GetInstallation(ctx, "demo", "nginx")
	require.NoError(t, err)
	assert.True(t, installation.IsInstalled(), "installed again")
	assert.True(t, installation.Status.Installed.After(firstInstall))

	runs, err := nemo.store.ListRuns(ctx, "demo", "nginx")
	require.NoError(t, err)
	assert.Len(t, runs, 3, "one CNAB claim per action")
}

func TestReferenceOf(t *testing.T) {
	t.Parallel()
	assert.Equal(t, storage.BundleReference{Repository: "ghcr.io/sntns/whoami", Version: "1.0.0"}, referenceOf("docker://ghcr.io/sntns/whoami:1.0.0", "1.0.0"), "as Porter writes it")
	assert.Equal(t, storage.BundleReference{Version: "1.0.0"}, referenceOf("oci:.artifacts/whoami.oci", "1.0.0"), "a layout is no repository")
	assert.Equal(t, storage.BundleReference{File: "whoami/bundle.json"}, referenceOf("whoami/bundle.json", "1.0.0"))
}

func TestRuns(t *testing.T) {
	t.Parallel()
	nemo, _, bundleFile, token := setup(t)
	ctx := context.Background()
	_, err := nemo.Install(ctx, ActionRequest{
		InstallationSpec: storage.InstallationSpec{Stack: "demo", Credentials: []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "path", Value: token}}}},
		BundleSource:     bundleFile,
		Target:           target.Target{Orchestrator: target.Compose},
	})
	require.NoError(t, err)
	_, err = nemo.Uninstall(ctx, ActionRequest{InstallationSpec: demoNginx})
	require.NoError(t, err)

	runs, err := nemo.Runs(ctx, "demo", "nginx")
	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, []string{"install", "uninstall"}, []string{runs[0].Action, runs[1].Action}, "oldest first")
	assert.Equal(t, "succeeded", runs[0].Status, "with its result")
	assert.Equal(t, []string{"token"}, runs[0].Credentials, "the credentials by name, from the claim's custom data")
	assert.Equal(t, target.Compose, runs[0].Target.Orchestrator)
}

// The records keep the TLS's sources as the action had them, files of the
// machine it ran on perhaps: an action reaches the target with the TLS of
// the manifest nemo has, for its address.
func TestTheManifestsTLSReachesTheTarget(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, token := setup(t)
	home := filepath.Join(filepath.Dir(bundleFile), "home")
	nemo.manifests = stack.NewStore(home)
	tlsAt := func(directory string) *target.TLS {
		file := func(name string) *valuesource.Source {
			return &valuesource.Source{Key: "path", Value: filepath.Join(directory, name)}
		}
		return &target.TLS{CA: file("ca.pem"), Cert: file("cert.pem"), Key: file("key.pem")}
	}
	deployment := func(tls *target.TLS) target.Target {
		return target.Target{Orchestrator: target.Swarm, Configuration: target.Configuration{Host: "tcp://vm1:2376", TLS: tls}}
	}

	_, err := nemo.Install(context.Background(), ActionRequest{
		InstallationSpec: storage.InstallationSpec{Stack: "demo", Credentials: []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "path", Value: token}}}},
		BundleSource:     bundleFile,
		Target:           deployment(tlsAt("/ci")),
	})
	require.NoError(t, err)
	stored, err := nemo.Installation(context.Background(), "demo", "nginx")
	require.NoError(t, err)
	assert.Equal(t, tlsAt("/ci"), stored.Target.TLS, "its sources recorded, as a credential's")

	require.NoError(t, nemo.manifests.Save(stack.Stack{
		Name: "demo", Target: deployment(tlsAt("/laptop")),
		Installations: []storage.InstallationSpec{{Name: "nginx", Bundle: storage.BundleReference{File: bundleFile}}},
	}, ""))
	_, err = nemo.Uninstall(context.Background(), ActionRequest{InstallationSpec: demoNginx})
	require.NoError(t, err)
	assert.Equal(t, tlsAt("/laptop"), fake.last().Target.TLS, "the manifest's, for its address")
}
