package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/valuesource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
)

func TestStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store := New(root)

	_, err := store.GetInstallation(ctx, "demo", "nginx")
	require.ErrorIs(t, err, storage.ErrNotFound)

	installed := time.Now()
	installation := storage.Installation{
		InstallationSpec: storage.InstallationSpec{
			Name:        "nginx",
			Stack:       "demo",
			Bundle:      storage.BundleReference{Repository: "localhost:5001/nginx", Version: "1.0.0"},
			Parameters:  map[string]string{"region": "eu5"},
			Credentials: []valuesource.Strategy{{Name: "token", Source: valuesource.Source{Key: "path", Value: "/tmp/token"}, Value: "s3cret"}},
		},
		Target: target.Target{Orchestrator: target.Compose},
		Status: storage.InstallationStatus{RunID: "01B", Action: cnab.ActionInstall, ResultStatus: cnab.StatusSucceeded, Installed: &installed},
	}
	require.NoError(t, store.SaveInstallation(ctx, installation))
	got, err := store.GetInstallation(ctx, "demo", "nginx")
	require.NoError(t, err)
	assert.Equal(t, storage.SchemaVersion, got.SchemaVersion)
	assert.Equal(t, "demo/nginx", got.ID())
	assert.Equal(t, installation.Parameters, got.Parameters)
	assert.Equal(t, "/tmp/token", got.Credentials[0].Source.Value)
	assert.Empty(t, got.Credentials[0].Value, "a credential's value is never written")
	assert.True(t, got.IsInstalled())

	listed, err := store.ListInstallations(ctx, "demo")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "demo/nginx", listed[0].ID())
	listed, err = store.ListInstallations(ctx, "other")
	require.NoError(t, err)
	assert.Empty(t, listed, "a stack nemo does not know has none")

	for _, id := range []string{"01B", "01A"} {
		require.NoError(t, store.SaveRun(ctx, "demo", "nginx", cnab.Claim{ID: id, Action: cnab.ActionInstall, Bundle: bundle.Bundle{Name: "nginx", Version: "1.0.0"}}))
	}
	run, err := store.GetRun(ctx, "demo", "nginx", got.Status.RunID)
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", run.Bundle.Version, "the bundle an installation last ran is in its run")
	_, err = store.GetRun(ctx, "demo", "nginx", "01Z")
	require.ErrorIs(t, err, storage.ErrNotFound)
	runs, err := store.ListRuns(ctx, "demo", "nginx")
	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, "01A", runs[0].ID, "oldest first")
	require.NoError(t, store.SaveResult(ctx, "demo", "nginx", cnab.Result{ID: "01C", ClaimID: "01A", Status: cnab.StatusSucceeded}, map[string]string{"url": "http://demo"}))
	results, err := store.ListResults(ctx, "demo", "nginx")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "01A", results[0].ClaimID)
	outputs, err := store.GetOutputs(ctx, "demo", "nginx", "01C")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"url": "http://demo"}, outputs)

	directory := filepath.Join(root, "demo", "installations", "nginx")
	for path, mode := range map[string]os.FileMode{
		directory: 0o700,
		filepath.Join(directory, "installation.json"):     0o600,
		filepath.Join(directory, "runs", "01A.json"):      0o600,
		filepath.Join(directory, "results", "01C.json"):   0o600,
		filepath.Join(directory, "outputs", "01C", "url"): 0o600,
	} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, mode, info.Mode().Perm(), path)
	}

	require.NoError(t, os.WriteFile(filepath.Join(directory, "installation.json"), []byte(`{"schemaVersion":"3","name":"nginx"}`), 0o600))
	_, err = store.GetInstallation(ctx, "demo", "nginx")
	require.ErrorContains(t, err, "schema 3, this nemo reads 4", "a document of another schema is not misread")
}

func TestStore_names(t *testing.T) {
	t.Parallel()
	store := New(t.TempDir())
	for _, stack := range []string{"..", "a/b", ""} {
		_, err := store.GetInstallation(context.Background(), stack, "nginx")
		assert.ErrorContains(t, err, "can't name a directory", stack)
	}
}

func TestDefaultRoot(t *testing.T) {
	t.Setenv(HomeVariable, "/srv/nemo")
	root, err := DefaultRoot()
	require.NoError(t, err)
	assert.Equal(t, "/srv/nemo", root)
}
