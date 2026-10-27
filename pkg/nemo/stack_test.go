package nemo

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/bundle/definition"
	"github.com/cnabio/cnab-go/valuesource"
	"github.com/cnabio/cnab-to-oci/relocation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/internal/testimage"
	nemobundle "github.com/sntns/nemo/pkg/bundle"
	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/progress"
	"github.com/sntns/nemo/pkg/stack"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
	"github.com/sntns/nemo/pkg/transfer"
)

// writeBundle writes a bundle that declares region and nemo-purge, and
// depends on the bundles named.
func writeBundle(t *testing.T, directory, name string, dependsOn ...string) string {
	t.Helper()
	custom := map[string]any{cnab.DockerExtension.Key: map[string]any{}}
	if len(dependsOn) > 0 {
		requires := map[string]any{}
		for _, dependency := range dependsOn {
			requires[dependency] = map[string]any{"bundle": "registry.example.com/bundles/" + dependency + ":1.0.0", "version": map[string]any{"ranges": []any{">=1.0.0"}}}
		}
		custom[cnab.DependenciesExtension.Key] = map[string]any{"requires": requires}
	}
	data, err := json.Marshal(bundle.Bundle{
		SchemaVersion:      "1.2.0",
		Name:               name,
		Version:            "1.0.0",
		InvocationImages:   []bundle.InvocationImage{{BaseImage: bundle.BaseImage{ImageType: "docker", Image: "gill:dev"}}},
		RequiredExtensions: []string{cnab.DockerExtension.Key},
		Custom:             custom,
		Definitions:        definition.Definitions{"string": &definition.Schema{Type: "string"}, "boolean": &definition.Schema{Type: "boolean"}},
		Parameters: map[string]bundle.Parameter{
			"region":            {Definition: "string", Destination: &bundle.Location{EnvironmentVariable: "REGION"}},
			cnab.ParameterPurge: {Definition: "boolean", ApplyTo: []string{"uninstall"}, Destination: &bundle.Location{EnvironmentVariable: "NEMO_PURGE"}},
		},
	})
	require.NoError(t, err)
	path := filepath.Join(directory, name+".json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestUpAndDown(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	ctx := context.Background()
	directory := filepath.Dir(bundleFile)
	database, web := writeBundle(t, directory, "database"), writeBundle(t, directory, "web", "database")

	actions := func(from int) []string {
		var result []string
		for _, request := range fake.requests[from:] {
			result = append(result, request.Action+" "+request.Bundle.Name)
		}
		return result
	}
	demo := stack.Stack{
		Name:       "demo",
		Target:     target.Target{Orchestrator: target.Compose},
		Parameters: map[string]string{"region": "eu5", "unknown": "x"},
		Installations: []storage.InstallationSpec{
			{Name: "web", Bundle: storage.BundleReference{File: web}},
			{Name: "database", Bundle: storage.BundleReference{File: database}, Parameters: map[string]string{"region": "eu6"}},
		},
	}

	result, err := nemo.Up(ctx, UpRequest{Stack: demo})
	require.NoError(t, err)
	assert.Equal(t, []string{"install database", "install web"}, actions(0), "dependencies first")
	assert.Len(t, result.Runs, 2)
	assert.Equal(t, map[string]string{"region": "eu6"}, fake.requests[0].Parameters, "the bundle's own parameters win")
	assert.Equal(t, map[string]string{"region": "eu5"}, fake.requests[1].Parameters, "the stack's, those the bundle declares")

	_, err = nemo.Up(ctx, UpRequest{Stack: demo})
	require.NoError(t, err)
	assert.Equal(t, []string{"upgrade database", "upgrade web"}, actions(2))

	demo.Installations = demo.Installations[1:]
	_, err = nemo.Up(ctx, UpRequest{Stack: demo})
	require.NoError(t, err)
	assert.Equal(t, []string{"upgrade database", "uninstall web"}, actions(4), "a bundle the stack no longer has is uninstalled")

	result, err = nemo.Down(ctx, DownRequest{Name: "demo", Purge: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"uninstall database"}, actions(6))
	assert.Equal(t, "true", fake.last().Parameters[cnab.ParameterPurge])
	assert.Len(t, result.Runs, 1)

	_, err = nemo.Down(ctx, DownRequest{Name: "demo"})
	require.EqualError(t, err, "stack demo has no installations")
}

func TestDownDependantsFirst(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	ctx := context.Background()
	directory := filepath.Dir(bundleFile)
	demo := stack.Stack{
		Name:   "demo",
		Target: target.Target{Orchestrator: target.Swarm},
		Installations: []storage.InstallationSpec{
			{Name: "database", Bundle: storage.BundleReference{File: writeBundle(t, directory, "database")}},
			{Name: "web", Bundle: storage.BundleReference{File: writeBundle(t, directory, "web", "database")}},
		},
	}
	_, err := nemo.Up(ctx, UpRequest{Stack: demo})
	require.NoError(t, err)

	_, err = nemo.Down(ctx, DownRequest{Name: "demo"})
	require.NoError(t, err)
	assert.Equal(t, "web", fake.requests[2].Bundle.Name)
	assert.Equal(t, "database", fake.requests[3].Bundle.Name)
}

// fakeRegistry holds bundles by their reference, and lists their tags.
type fakeRegistry map[string]string

func (registry fakeRegistry) ListTags(ctx context.Context, repository cnab.OCIReference) ([]string, error) {
	var tags []string
	for reference := range registry {
		if repositoryOf, tag, _ := strings.Cut(reference, ":"); repositoryOf == repository.Named.Name() {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}

func TestUpResolvesAMissingDependency(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	directory := filepath.Dir(bundleFile)
	registry := fakeRegistry{
		"registry.example.com/bundles/database:1.0.0":     writeBundle(t, directory, "database"),
		"registry.example.com/bundles/database:1.2.0":     writeBundle(t, directory, "database"),
		"registry.example.com/bundles/database:2.0.0-rc1": writeBundle(t, directory, "database"),
	}
	nemo.tags = registry
	nemo.load = func(ctx context.Context, source string) (cnab.BundleReference, error) {
		if reference, ok := strings.CutPrefix(source, "docker://"); ok {
			file, found := registry[reference]
			if !found {
				return cnab.BundleReference{}, fmt.Errorf("%s not found", reference)
			}
			return LoadBundle(ctx, file)
		}
		return LoadBundle(ctx, source)
	}
	demo := stack.Stack{
		Name:          "demo",
		Target:        target.Target{Orchestrator: target.Compose},
		Installations: []storage.InstallationSpec{{Name: "web", Bundle: storage.BundleReference{File: writeBundle(t, directory, "web", "database")}}},
	}

	_, err := nemo.Up(context.Background(), UpRequest{Stack: demo})
	require.NoError(t, err)
	assert.Equal(t, "database", fake.requests[0].Bundle.Name, "the dependency, from its registry, first")
	assert.Equal(t, "web", fake.requests[1].Bundle.Name)

	nemo.tags = fakeRegistry{}
	_, err = nemo.Up(context.Background(), UpRequest{Stack: stack.Stack{Name: "other", Target: demo.Target, Installations: demo.Installations}})
	require.ErrorContains(t, err, "stack other: dependency registry.example.com/bundles/database:1.0.0")
}

func TestUpFromARemote(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	ctx := context.Background()
	directory := filepath.Dir(bundleFile)

	// A bundle pushed to an OCI layout, as nemo bundle build -o oci:… does.
	installer := filepath.Join(directory, "gill.oci")
	testimage.Write(t, installer)
	installerLocation := nemobundle.Location{Transport: nemobundle.Layout, Path: installer}
	descriptor, err := installerLocation.Resolve(ctx)
	require.NoError(t, err)
	extended, err := cnab.LoadBundle(writeBundle(t, directory, "database"))
	require.NoError(t, err)
	extended.InvocationImages[0].BaseImage = bundle.BaseImage{ImageType: "docker", Image: "gill:latest", Digest: descriptor.Digest.String(), Size: uint64(descriptor.Size), MediaType: descriptor.MediaType}
	destination := nemobundle.Location{Transport: nemobundle.Layout, Path: filepath.Join(directory, "artifacts", "database.oci")}
	_, err = nemobundle.Push(ctx, extended, []transfer.Image{installerLocation.Image("gill:latest")}, destination)
	require.NoError(t, err)

	_, err = nemo.Up(ctx, UpRequest{Stack: stack.Stack{
		Name:          "demo",
		Target:        target.Target{Orchestrator: target.Compose},
		Remote:        "oci:" + filepath.Join(directory, "artifacts", "{{ .Component }}.oci"),
		Installations: []storage.InstallationSpec{{Name: "database", Bundle: storage.BundleReference{Version: "1.0.0"}}},
	}})
	require.NoError(t, err)
	assert.Equal(t, "database", fake.last().Bundle.Name, "pulled from the layout")
	assert.Nil(t, fake.last().Relocation, "a layout's images are on no registry")

	_, err = nemo.Up(ctx, UpRequest{Stack: stack.Stack{
		Name:          "demo",
		Target:        target.Target{Orchestrator: target.Compose},
		Remote:        "oci:" + filepath.Join(directory, "artifacts", "{{ .Component }}.oci"),
		Installations: []storage.InstallationSpec{{Name: "web", Bundle: storage.BundleReference{Version: "1.0.0"}}},
	}})
	require.ErrorContains(t, err, "web", "no bundle at the remote's location of web")
}

func TestRelocationIsKept(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, token := setup(t)
	ctx := context.Background()
	extended, err := cnab.LoadBundle(bundleFile)
	require.NoError(t, err)
	relocated := relocation.ImageRelocationMap{"gill:dev": "ghcr.io/sntns/nginx@sha256:" + strings.Repeat("a", 64)}

	_, err = nemo.act(ctx, cnab.ActionInstall, ActionRequest{
		InstallationSpec: storage.InstallationSpec{Stack: "demo", Credentials: []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "path", Value: token}}}},
		Target:           target.Target{Orchestrator: target.Compose},
	}, &cnab.BundleReference{Definition: extended, RelocationMap: relocated})
	require.NoError(t, err)
	assert.Equal(t, relocated, fake.last().Relocation, "the runtime gets it")

	_, err = nemo.Upgrade(ctx, ActionRequest{InstallationSpec: demoNginx})
	require.NoError(t, err)
	assert.Equal(t, relocated, fake.last().Relocation, "the installation kept it")
}

func TestUp_stackCredentials(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, token := setup(t)
	demo := stack.Stack{
		Name:   "demo",
		Target: target.Target{Orchestrator: target.Compose},
		Credentials: []valuesource.Strategy{
			{Name: "token", Source: valuesource.Source{Key: "path", Value: token}},
			{Name: "unused", Source: valuesource.Source{Key: "value", Value: "x"}},
		},
		Installations: []storage.InstallationSpec{
			{Name: "nginx", Bundle: storage.BundleReference{File: bundleFile}},
			{Name: "database", Bundle: storage.BundleReference{File: writeBundle(t, filepath.Dir(bundleFile), "database")}},
		},
	}
	_, err := nemo.Up(context.Background(), UpRequest{Stack: demo})
	require.NoError(t, err)
	require.Len(t, fake.requests, 2)
	assert.Equal(t, valuesource.Set{"token": "s3cret"}, fake.requests[0].Credentials, "the stack's credentials its bundle declares")
	assert.Empty(t, fake.requests[1].Credentials, "none for a bundle that declares none")
}

func TestUpResolvesADependencyFromTheRemote(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	ctx := context.Background()
	directory := filepath.Dir(bundleFile)
	built := map[string]string{"oci:/build/database.oci": writeBundle(t, directory, "database")}
	version := "1.0.0"
	nemo.tags = fakeRegistry{}
	nemo.load = func(ctx context.Context, source string) (cnab.BundleReference, error) {
		file, found := built[source]
		if !found && strings.Contains(source, ":") {
			return cnab.BundleReference{}, fmt.Errorf("%s not found", source)
		}
		loaded, err := LoadBundle(ctx, cmp.Or(file, source))
		if found {
			loaded.Definition.Version = version
		}
		return loaded, err
	}
	demo := stack.Stack{
		Name:          "demo",
		Target:        target.Target{Orchestrator: target.Compose},
		Remote:        "oci:/build/{{ .Component }}.oci",
		Installations: []storage.InstallationSpec{{Name: "web", Bundle: storage.BundleReference{File: writeBundle(t, directory, "web", "database")}}},
	}

	_, err := nemo.Up(ctx, UpRequest{Stack: demo})
	require.NoError(t, err)
	assert.Equal(t, "database", fake.requests[0].Bundle.Name, "the dependency first")
	database, err := nemo.store.GetInstallation(ctx, "demo", "database")
	require.NoError(t, err)
	assert.Equal(t, "oci:/build/database.oci", database.Status.BundleReference, "from the stack's remote, with no registry")

	version = "0.1.0"
	_, err = nemo.Up(ctx, UpRequest{Stack: stack.Stack{Name: "other", Target: demo.Target, Remote: demo.Remote, Installations: demo.Installations}})
	require.ErrorContains(t, err, "the stack's remote has it at oci:/build/database.oci: version 0.1.0 is in none of >=1.0.0", "not the registry's instead")
}

func TestUpAnnouncesInstallations(t *testing.T) {
	t.Parallel()
	nemo, _, bundleFile, _ := setup(t)
	directory := filepath.Dir(bundleFile)
	var announced []string
	events := progress.SinkFunc(func(event progress.Event) {
		if event.Group == progress.Installation {
			announced = append(announced, fmt.Sprintf("%s %s %s", event.Installation, event.State, event.Message))
		}
	})
	demo := stack.Stack{
		Name:   "demo",
		Target: target.Target{Orchestrator: target.Compose},
		Installations: []storage.InstallationSpec{
			{Name: "web", Bundle: storage.BundleReference{File: writeBundle(t, directory, "web", "database")}},
			{Name: "database", Bundle: storage.BundleReference{File: writeBundle(t, directory, "database")}},
		},
	}
	_, err := nemo.Up(context.Background(), UpRequest{Stack: demo, Progress: events})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"demo/database pending install", "demo/web pending install",
		"demo/database working install", "demo/database done install",
		"demo/web working install", "demo/web done install",
	}, announced, "every installation waits first, in the order of the dependencies")
}

func TestUpOnly(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	directory := filepath.Dir(bundleFile)
	demo := stack.Stack{
		Name:   "demo",
		Target: target.Target{Orchestrator: target.Compose},
		Installations: []storage.InstallationSpec{
			{Name: "web", Bundle: storage.BundleReference{File: writeBundle(t, directory, "web", "database")}},
			{Name: "database", Bundle: storage.BundleReference{File: writeBundle(t, directory, "database")}},
			{Name: "cache", Bundle: storage.BundleReference{File: writeBundle(t, directory, "cache")}},
		},
	}
	_, err := nemo.Up(context.Background(), UpRequest{Stack: demo})
	require.NoError(t, err)
	require.Len(t, fake.requests, 3)

	demo.Installations = demo.Installations[:2]
	_, err = nemo.Up(context.Background(), UpRequest{Stack: demo, Only: []string{"web"}})
	require.NoError(t, err)
	require.Len(t, fake.requests, 4, "web alone: its database is left as it is, and the cache the manifest no longer has is not uninstalled")
	assert.Equal(t, "upgrade web", fake.last().Action+" "+fake.last().Bundle.Name)

	_, err = nemo.Up(context.Background(), UpRequest{Stack: demo, Only: []string{"cache"}})
	require.EqualError(t, err, "stack demo has no installation cache")

	_, err = nemo.Down(context.Background(), DownRequest{Name: "demo"})
	require.NoError(t, err)
	before := len(fake.requests)
	_, err = nemo.Up(context.Background(), UpRequest{Stack: demo, Only: []string{"web"}})
	require.NoError(t, err)
	var actions []string
	for _, request := range fake.requests[before:] {
		actions = append(actions, request.Action+" "+request.Bundle.Name)
	}
	assert.Equal(t, []string{"install database", "install web"}, actions, "web alone, with its database not installed")
}

func TestDown_failedInstall(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	directory := filepath.Dir(bundleFile)
	demo := stack.Stack{
		Name:   "demo",
		Target: target.Target{Orchestrator: target.Compose},
		Installations: []storage.InstallationSpec{
			{Name: "web", Bundle: storage.BundleReference{File: writeBundle(t, directory, "web", "database")}},
			{Name: "database", Bundle: storage.BundleReference{File: writeBundle(t, directory, "database")}},
		},
	}
	run := func(action func() error) []string {
		before := len(fake.requests)
		_ = action()
		var done []string
		for _, request := range fake.requests[before:] {
			done = append(done, request.Action+" "+request.Bundle.Name)
		}
		return done
	}
	up := func() error { _, err := nemo.Up(context.Background(), UpRequest{Stack: demo}); return err }
	down := func() error { _, err := nemo.Down(context.Background(), DownRequest{Name: "demo"}); return err }

	fake.failing = map[string]bool{"web": true}
	assert.Equal(t, []string{"install database", "install web"}, run(up))
	fake.failing = nil
	assert.Equal(t, []string{"uninstall web", "uninstall database"}, run(down), "an install that failed may have left services: down removes them too")

	fake.failing = map[string]bool{"web": true}
	assert.Equal(t, []string{"install database", "install web"}, run(up))
	fake.failing = nil
	assert.Equal(t, []string{"upgrade database", "install web"}, run(up), "and up installs it again, rather than upgrading it")
}
