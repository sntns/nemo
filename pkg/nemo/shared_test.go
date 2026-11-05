package nemo

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnabio/cnab-go/valuesource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/progress"
	"github.com/sntns/nemo/pkg/runtime"
	"github.com/sntns/nemo/pkg/stack"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
)

// composed gives the bundle written at path a compose project, as the
// bundles nemo builds have one.
func composed(t *testing.T, path string, project map[string]any) string {
	t.Helper()
	extended, err := cnab.LoadBundle(path)
	require.NoError(t, err)
	extended.Custom[cnab.ComposeExtension.Key] = map[string]any{
		"schemaVersion": cnab.ComposeSchemaVersion,
		"compose":       []any{map[string]any{"name": "compose.yaml", "config": project}},
	}
	data, err := json.Marshal(extended.Bundle)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// onNetworks is a project of one service on the networks.
func onNetworks(service string, networks ...string) map[string]any {
	declared := map[string]any{}
	for _, network := range networks {
		declared[network] = map[string]any{}
	}
	return map[string]any{
		"services": map[string]any{service: map[string]any{"image": "busybox:1.37", "networks": networks}},
		"networks": declared,
	}
}

// announce emits, as an installer does, a done event for each network the
// bundle's project declares, as the stack names it.
func announce(request runtime.Request) {
	if request.Bundle.Compose == nil || request.Progress == nil || request.Action == cnab.ActionUninstall {
		return
	}
	for _, document := range request.Bundle.Compose.Documents {
		networks, _ := document.Config["networks"].(map[string]any)
		for _, key := range slices.Sorted(maps.Keys(networks)) {
			request.Progress.Emit(progress.Event{Group: "networks", Item: request.Stack + "_" + key, State: progress.Done, Message: "created"})
		}
	}
}

// journal records what happens, in order.
type journal struct {
	mutex   sync.Mutex
	entries []string
	events  []progress.Event
}

func (journal *journal) add(entry string) {
	journal.mutex.Lock()
	defer journal.mutex.Unlock()
	journal.entries = append(journal.entries, entry)
}

func (journal *journal) Emit(event progress.Event) {
	journal.mutex.Lock()
	defer journal.mutex.Unlock()
	journal.events = append(journal.events, event)
}

func (journal *journal) index(entry string) int {
	journal.mutex.Lock()
	defer journal.mutex.Unlock()
	return slices.Index(journal.entries, entry)
}

// A network two installations of five share is created by the first: the
// second starts once it is announced, not once the first ends, and the
// three others don't wait for it. On the way down, the creator goes last.
func TestUpSharesAnObjectBetweenItsUsers(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	directory := filepath.Dir(bundleFile)
	demo := stack.Stack{Name: "demo", Target: target.Target{Orchestrator: target.Compose}}
	for name, networks := range map[string][]string{"alpha": {"mesh"}, "bravo": {"mesh"}, "charlie": {"own-c"}, "delta": {"own-d"}, "echo": {"own-e"}} {
		file := composed(t, writeBundle(t, directory, name), onNetworks(name, networks...))
		demo.Installations = append(demo.Installations, storage.InstallationSpec{Name: name, Bundle: storage.BundleReference{File: file}})
	}

	happened := &journal{}
	bravoStarted := make(chan struct{})
	nemo.execute = func(ctx context.Context, request runtime.Request) (runtime.Run, error) {
		name := request.Action + " " + request.Bundle.Name
		happened.add("start " + name)
		announce(request)
		switch name {
		case "install alpha":
			happened.add("announced demo_mesh")
			select {
			case <-bravoStarted:
			case <-time.After(5 * time.Second):
				return runtime.Run{}, errors.New("bravo waited for alpha to end, not for its network")
			}
		case "install bravo":
			close(bravoStarted)
		}
		run, err := fake.execute(ctx, request)
		happened.add("end " + name)
		return run, err
	}

	_, err := nemo.Up(context.Background(), UpRequest{Stack: demo, Progress: happened})
	require.NoError(t, err)
	assert.Less(t, happened.index("announced demo_mesh"), happened.index("start install bravo"))
	assert.Less(t, happened.index("start install bravo"), happened.index("end install alpha"), "bravo did not wait for alpha to end")
	assert.Contains(t, happened.events, progress.Event{Installation: "demo", Group: "networks", Item: "demo_mesh", State: progress.Pending, Message: "shared by 2 installations"}, "the stack's row of the network")
	assert.Contains(t, happened.events, progress.Event{Installation: "demo", Group: "networks", Item: "demo_mesh", State: progress.Done, Message: "created"}, "told by its creator")
	for _, event := range happened.events {
		assert.False(t, event.Installation == "demo" && strings.HasPrefix(event.Item, "demo_own"), "a network of one installation is its own: %v", event)
	}

	gone := &journal{}
	_, err = nemo.Down(context.Background(), DownRequest{Name: "demo", Progress: gone})
	require.NoError(t, err)
	assert.Less(t, happened.index("end uninstall bravo"), happened.index("start uninstall alpha"), "the creator goes after the others")
	var announced []string
	for _, event := range gone.events {
		if event.State == progress.Pending {
			announced = append(announced, strings.TrimPrefix(event.Installation+" "+event.Item, "demo/"))
		}
	}
	assert.Equal(t, []string{"bravo ", "charlie ", "delta ", "echo ", "alpha ", "demo demo_mesh"}, announced, "shown in the order they go, by name within a level, the shared network last")
}

func TestUpRefusesWhatCannotBeShared(t *testing.T) {
	t.Parallel()
	for name, projects := range map[string]struct {
		alpha, bravo map[string]any
		refused      string
	}{
		"a service": {
			alpha:   map[string]any{"services": map[string]any{"web": map[string]any{"image": "busybox:1.37"}}},
			bravo:   map[string]any{"services": map[string]any{"web": map[string]any{"image": "busybox:1.37"}}},
			refused: "service demo_web is declared by alpha and bravo",
		},
		"a port": {
			alpha:   map[string]any{"services": map[string]any{"web": map[string]any{"image": "busybox:1.37", "ports": []any{"8080:80"}}}},
			bravo:   map[string]any{"services": map[string]any{"api": map[string]any{"image": "busybox:1.37", "ports": []any{"8080:8080"}}}},
			refused: "port *:8080/tcp is published by alpha and bravo",
		},
		"a volume made otherwise": {
			alpha:   map[string]any{"services": map[string]any{"web": map[string]any{"image": "busybox:1.37"}}, "volumes": map[string]any{"data": map[string]any{"driver": "local"}}},
			bravo:   map[string]any{"services": map[string]any{"api": map[string]any{"image": "busybox:1.37"}}, "volumes": map[string]any{"data": map[string]any{"driver": "nfs"}}},
			refused: "volume demo_data: alpha and bravo make it with other drivers or options",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			nemo, fake, bundleFile, _ := setup(t)
			directory := filepath.Dir(bundleFile)
			demo := stack.Stack{Name: "demo", Target: target.Target{Orchestrator: target.Compose}, Installations: []storage.InstallationSpec{
				{Name: "alpha", Bundle: storage.BundleReference{File: composed(t, writeBundle(t, directory, "alpha"), projects.alpha)}},
				{Name: "bravo", Bundle: storage.BundleReference{File: composed(t, writeBundle(t, directory, "bravo"), projects.bravo)}},
			}}
			_, err := nemo.Up(context.Background(), UpRequest{Stack: demo})
			require.ErrorContains(t, err, projects.refused)
			assert.Empty(t, fake.requests, "before anything is touched")
		})
	}
}

// An installation the stack drops keeps, even with purge, a volume one it
// keeps still uses.
func TestUpKeepsTheVolumeAKeptInstallationUses(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	directory := filepath.Dir(bundleFile)
	withData := func(service string) map[string]any {
		return map[string]any{"services": map[string]any{service: map[string]any{"image": "busybox:1.37", "volumes": []any{"data:/data"}}}, "volumes": map[string]any{"data": map[string]any{}}}
	}
	demo := stack.Stack{Name: "demo", Target: target.Target{Orchestrator: target.Compose}, Installations: []storage.InstallationSpec{
		{Name: "alpha", Bundle: storage.BundleReference{File: composed(t, writeBundle(t, directory, "alpha"), withData("web"))}},
		{Name: "bravo", Bundle: storage.BundleReference{File: composed(t, writeBundle(t, directory, "bravo"), withData("api"))}},
	}}
	_, err := nemo.Up(context.Background(), UpRequest{Stack: demo})
	require.NoError(t, err)

	happened := &journal{}
	demo.Installations = demo.Installations[:1]
	_, err = nemo.Up(context.Background(), UpRequest{Stack: demo, Purge: true, Progress: happened})
	require.NoError(t, err)
	assert.Equal(t, "uninstall bravo", fake.last().Action+" "+fake.last().Bundle.Name)
	assert.NotContains(t, fake.last().Parameters, cnab.ParameterPurge, "its volumes are kept")
	assert.Contains(t, happened.events, progress.Event{Installation: "demo/bravo", Group: "volumes", Item: "demo_data", State: progress.Warning, Message: "kept: alpha still uses it"})
}

// The credentials are read once, before anything is touched, and the action
// is given them.
func TestUpReadsTheCredentialsOnce(t *testing.T) {
	t.Parallel()
	nemo, fake, bundleFile, _ := setup(t)
	directory := t.TempDir()
	count, script := filepath.Join(directory, "count"), filepath.Join(directory, "token.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho read >> "+count+"\nprintf s3cret\n"), 0o700))
	demo := stack.Stack{
		Name:   "demo",
		Target: target.Target{Orchestrator: target.Compose},
		Credentials: []valuesource.Strategy{
			{Name: "token", Source: valuesource.Source{Key: "command", Value: script}},
		},
		Installations: []storage.InstallationSpec{{Name: "nginx", Bundle: storage.BundleReference{File: bundleFile}}},
	}
	_, err := nemo.Up(context.Background(), UpRequest{Stack: demo})
	require.NoError(t, err)
	data, err := os.ReadFile(count)
	require.NoError(t, err)
	assert.Equal(t, "read\n", string(data))
	assert.Equal(t, "s3cret", fake.last().Credentials["token"])
}

// Services that name no network join the project's default one, which two
// installations then share.
func TestUpSharesTheDefaultNetwork(t *testing.T) {
	t.Parallel()
	nemo, _, bundleFile, _ := setup(t)
	directory := filepath.Dir(bundleFile)
	alone := func(service string) map[string]any {
		return map[string]any{"services": map[string]any{service: map[string]any{"image": "busybox:1.37"}}}
	}
	demo := stack.Stack{Name: "demo", Target: target.Target{Orchestrator: target.Swarm}, Installations: []storage.InstallationSpec{
		{Name: "alpha", Bundle: storage.BundleReference{File: composed(t, writeBundle(t, directory, "alpha"), alone("web"))}},
		{Name: "bravo", Bundle: storage.BundleReference{File: composed(t, writeBundle(t, directory, "bravo"), alone("api"))}},
	}}
	happened := &journal{}
	_, err := nemo.Up(context.Background(), UpRequest{Stack: demo, Progress: happened})
	require.NoError(t, err)
	assert.Contains(t, happened.events, progress.Event{Installation: "demo", Group: "networks", Item: "demo_default", State: progress.Pending, Message: "shared by 2 installations"})
}
