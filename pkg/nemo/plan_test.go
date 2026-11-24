package nemo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/secrets/host"
	"github.com/cnabio/cnab-go/valuesource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/stack"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
)

// Before anything runs, Plan tells the objects of the stack as Up records
// them, step after step: a new stack whose installations share a network, the
// same stack again, a volume added, a secret removed, a config whose content
// changes, and an installation gone. Plan runs nothing and writes nothing.
func TestPlan_objectsAsUpRecordsThem(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	directory := filepath.Dir(bundleFile)
	ctx := context.Background()

	type alpha struct {
		volume  bool
		setting string
	}
	type bravo struct {
		present, secret bool
	}
	stackOf := func(step string, a alpha, b bravo) stack.Stack {
		web := map[string]any{"image": "busybox:1.37", "networks": []any{"mesh"}, "configs": []any{"settings"}}
		project := map[string]any{
			"services": map[string]any{"web": web},
			"networks": map[string]any{"mesh": map[string]any{}},
			"configs":  map[string]any{"settings": map[string]any{"content": a.setting}},
		}
		if a.volume {
			web["volumes"] = []any{"data:/data"}
			project["volumes"] = map[string]any{"data": map[string]any{}}
		}
		demo := stack.Stack{
			Name:        "demo",
			Target:      target.Target{Orchestrator: target.Swarm},
			Credentials: []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: host.SourceValue, Value: "s3cret"}}},
			Installations: []storage.InstallationSpec{
				{Name: "alpha", Bundle: storage.BundleReference{File: composed(t, writeBundle(t, filepath.Join(directory, step), "alpha"), project)}},
			},
		}
		if b.present {
			api := map[string]any{"image": "busybox:1.37", "networks": []any{"mesh"}}
			project := map[string]any{"services": map[string]any{"api": api}, "networks": map[string]any{"mesh": map[string]any{}}}
			if b.secret {
				api["secrets"] = []any{"token"}
				project["secrets"] = map[string]any{"token": map[string]any{"environment": "TOKEN"}}
			}
			file := withCredential(t, composed(t, writeBundle(t, filepath.Join(directory, step), "bravo"), project), "token", "TOKEN")
			demo.Installations = append(demo.Installations, storage.InstallationSpec{Name: "bravo", Bundle: storage.BundleReference{File: file}})
		}
		return demo
	}

	steps := []struct {
		name     string
		alpha    alpha
		bravo    bravo
		expected []string
	}{
		{"new", alpha{setting: "level: info"}, bravo{present: true, secret: true}, []string{
			"network demo_mesh: alpha bravo", "config demo_settings: alpha", "secret demo_token: bravo",
		}},
		{"same", alpha{setting: "level: info"}, bravo{present: true, secret: true}, []string{
			"network demo_mesh: alpha bravo", "config demo_settings: alpha", "secret demo_token: bravo",
		}},
		{"volume-added", alpha{volume: true, setting: "level: info"}, bravo{present: true, secret: true}, []string{
			"volume demo_data: alpha", "network demo_mesh: alpha bravo", "config demo_settings: alpha", "secret demo_token: bravo",
		}},
		{"secret-removed", alpha{volume: true, setting: "level: info"}, bravo{present: true}, []string{
			"volume demo_data: alpha", "network demo_mesh: alpha bravo", "config demo_settings: alpha",
		}},
		{"config-changed", alpha{volume: true, setting: "level: debug"}, bravo{present: true}, []string{
			"volume demo_data: alpha", "network demo_mesh: alpha bravo", "config demo_settings: alpha",
		}},
		{"bravo-gone", alpha{volume: true, setting: "level: debug"}, bravo{}, []string{
			"volume demo_data: alpha", "network demo_mesh: alpha", "config demo_settings: alpha",
		}},
	}
	var settings []string
	for _, step := range steps {
		require.NoError(t, os.MkdirAll(filepath.Join(directory, step.name), 0o700))
		demo := stackOf(step.name, step.alpha, step.bravo)
		before, requests := recordsOf(t, nemo, "demo"), len(fake.requests)

		planned, err := nemo.Plan(ctx, UpRequest{Stack: demo})
		require.NoError(t, err, step.name)
		assert.Equal(t, requests, len(fake.requests), "%s: the plan runs nothing", step.name)
		assert.Equal(t, before, recordsOf(t, nemo, "demo"), "%s: the plan writes nothing", step.name)
		assert.Equal(t, step.expected, described(planned.Objects), step.name)

		_, err = nemo.Up(ctx, UpRequest{Stack: demo})
		require.NoError(t, err, step.name)
		recorded, err := nemo.store.ListInstallations(ctx, "demo")
		require.NoError(t, err)
		assert.Equal(t, storage.Objects(recorded), planned.Objects, "%s: the objects the plan told are those Up recorded", step.name)
		for _, object := range planned.Objects {
			if object.Kind == storage.KindConfig {
				settings = append(settings, object.Name)
			}
		}
	}
	assert.NotEqual(t, settings[len(settings)-1], settings[0], "a config whose content changes is another config, under the hash of its content")
}

// Plan refuses what Up would refuse, before anything runs: here a service
// two installations declare.
func TestPlan_refusesWhatUpRefuses(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	directory := filepath.Dir(bundleFile)
	demo := stack.Stack{Name: "demo", Target: target.Target{Orchestrator: target.Compose}}
	for _, name := range []string{"alpha", "bravo"} {
		file := composed(t, writeBundle(t, directory, name), map[string]any{"services": map[string]any{"web": map[string]any{"image": "busybox:1.37"}}})
		demo.Installations = append(demo.Installations, storage.InstallationSpec{Name: name, Bundle: storage.BundleReference{File: file}})
	}
	_, err := nemo.Plan(context.Background(), UpRequest{Stack: demo})
	require.ErrorContains(t, err, "service demo_web is declared by alpha and bravo")
	assert.Empty(t, fake.requests)
}

// withCredential gives the bundle written at path a credential, read from
// the environment variable, as a bundle whose project reads a secret there.
func withCredential(t *testing.T, path, name, variable string) string {
	t.Helper()
	extended, err := cnab.LoadBundle(path)
	require.NoError(t, err)
	extended.Credentials = map[string]bundle.Credential{name: {Location: bundle.Location{EnvironmentVariable: variable}, ApplyTo: []string{cnab.ActionInstall, cnab.ActionUpgrade}}}
	data, err := json.Marshal(extended.Bundle)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// recordsOf is every record of the stack's installations, as JSON.
func recordsOf(t *testing.T, nemo *Nemo, stackName string) string {
	t.Helper()
	installations, err := nemo.store.ListInstallations(context.Background(), stackName)
	require.NoError(t, err)
	data, err := json.Marshal(installations)
	require.NoError(t, err)
	return string(data)
}

// contentHash is the hash of its content a config's or a secret's name ends with.
var contentHash = regexp.MustCompile(`-[0-9a-f]{14}$`)

// described is each object as KIND NAME: USERS, a config's or a secret's
// name without the hash of its content.
func described(objects []storage.ObjectUse) []string {
	var lines []string
	for _, object := range objects {
		lines = append(lines, fmt.Sprintf("%s %s: %s", object.Kind, contentHash.ReplaceAllString(object.Name, ""), strings.Join(object.Users, " ")))
	}
	return lines
}
