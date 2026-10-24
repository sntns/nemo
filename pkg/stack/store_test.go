package stack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
)

func TestStore(t *testing.T) {
	t.Parallel()
	store := NewStore(t.TempDir())
	demo := Stack{
		Name:          "demo",
		Target:        target.Target{Orchestrator: target.Swarm},
		Remote:        "docker://ghcr.io/sntns/{{ .Component }}:{{ .Version }}",
		Parameters:    map[string]string{"gateway-host": "sntns.dev"},
		Installations: []storage.InstallationSpec{{Name: "whoami", Stack: "demo", Bundle: storage.BundleReference{Version: "1.0.0"}}},
	}

	current, err := store.Current()
	require.NoError(t, err)
	assert.Empty(t, current, "no stack is current yet")

	require.NoError(t, store.Save(demo, ""))
	path, err := store.Path("demo")
	require.NoError(t, err)
	assert.Equal(t, "manifest.yaml", filepath.Base(path), "YAML for a new stack")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `name: demo
target:
  orchestrator: swarm
remote: docker://ghcr.io/sntns/{{ .Component }}:{{ .Version }}
parameters:
  gateway-host: sntns.dev
installations:
  - name: whoami
    bundle:
      version: 1.0.0
`, string(data), "as people write it")
	loaded, err := store.Load("demo")
	require.NoError(t, err)
	assert.Equal(t, demo, loaded)

	require.NoError(t, store.Save(demo, JSON))
	path, err = store.Path("demo")
	require.NoError(t, err)
	assert.Equal(t, "manifest.json", filepath.Base(path), "as preferred, the YAML one gone")
	require.NoError(t, store.Save(demo, ""))
	path, _ = store.Path("demo")
	assert.Equal(t, "manifest.json", filepath.Base(path), "it keeps its format")

	require.NoError(t, os.WriteFile(filepath.Join(store.root, "edge.yaml"), []byte("address: localhost\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(store.root, "cache"), 0o700))
	names, err := store.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"demo"}, names, "the files of another tool in the home are no stacks")

	require.NoError(t, store.Use("demo"))
	current, _ = store.Current()
	assert.Equal(t, "demo", current)
	require.ErrorIs(t, store.Use("other"), storage.ErrNotFound)
	names, err = store.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"demo"}, names)

	require.NoError(t, store.Remove("demo"))
	current, _ = store.Current()
	assert.Empty(t, current, "the current stack was removed")
	names, _ = store.List()
	assert.Empty(t, names)
	require.Error(t, store.Save(Stack{Name: "../x"}, ""))
}
