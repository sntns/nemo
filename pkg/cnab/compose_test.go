package cnab

import (
	"context"
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeExtension(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		compose := Compose{
			SchemaVersion: "1.0.0",
			Documents: []ComposeDocument{
				{Name: "compose.yaml", Config: map[string]any{"services": map[string]any{"web": map[string]any{"image": "nginx"}}}},
			},
		}
		extendedBundle := testBundle(t, func(bundle *bundle.Bundle) {
			require.NoError(t, ComposeExtension.Write(bundle, compose))
		})
		require.NotNil(t, extendedBundle.Compose)
		assert.Equal(t, compose, *extendedBundle.Compose)
	})

	t.Run("against its schema", func(t *testing.T) {
		t.Parallel()
		bundle := testBundle(t, nil).Bundle
		bundle.Custom[ComposeExtension.Key] = map[string]any{"schemaVersion": "1.0.0", "compose": []any{}}
		_, err := NewBundle(bundle)
		require.ErrorContains(t, err, "io.nemo.compose:")
	})
}

func TestCompose_LoadLeavesTheDocuments(t *testing.T) {
	t.Parallel()
	compose := Compose{Documents: []ComposeDocument{{Config: map[string]any{
		"services": map[string]any{"web": map[string]any{"image": "nginx:${TAG}", "ports": []any{"8080:80"}}},
	}}}}

	project, err := compose.Load(context.Background(), "demo", "/", nil)
	require.NoError(t, err)
	assert.Equal(t, "nginx:${TAG}", project.Services["web"].Image, "not interpolated")
	assert.Equal(t, []any{"8080:80"}, compose.Documents[0].Config["services"].(map[string]any)["web"].(map[string]any)["ports"], "the loader normalized a copy")

	project, err = compose.Load(context.Background(), "demo", "/", map[string]string{"TAG": "1.27"})
	require.NoError(t, err)
	assert.Equal(t, "nginx:1.27", project.Services["web"].Image)
}

// The documents of a bundle are JSON: their numbers reach Load as float64,
// where a compose file gives the loader an int.
func TestCompose_LoadTakesTheNumbersOfAJSONDocument(t *testing.T) {
	t.Parallel()
	compose := Compose{Documents: []ComposeDocument{{Config: map[string]any{
		"services": map[string]any{"web": map[string]any{
			"image":   "nginx",
			"ulimits": map[string]any{"nofile": map[string]any{"soft": 1024.0, "hard": 2048.0}, "nproc": 512.0},
			"ports":   []any{map[string]any{"target": 80.0, "published": 8080.0}},
			"configs": []any{map[string]any{"source": "app", "mode": 288.0}},
			"deploy":  map[string]any{"update_config": map[string]any{"max_failure_ratio": 0.5}},
		}},
		"configs": map[string]any{"app": map[string]any{"content": "hello"}},
	}}}}

	project, err := compose.Load(context.Background(), "demo", "/", map[string]string{})
	require.NoError(t, err)
	web := project.Services["web"]
	assert.Equal(t, 1024, web.Ulimits["nofile"].Soft)
	assert.Equal(t, 2048, web.Ulimits["nofile"].Hard)
	assert.Equal(t, 512, web.Ulimits["nproc"].Single)
	assert.Equal(t, "8080", web.Ports[0].Published)
	assert.Equal(t, uint32(80), web.Ports[0].Target)
	if assert.NotNil(t, web.Configs[0].Mode) {
		assert.Equal(t, types.FileMode(0o440), *web.Configs[0].Mode)
	}
	assert.InDelta(t, 0.5, web.Deploy.UpdateConfig.MaxFailureRatio, 0.001)
}
