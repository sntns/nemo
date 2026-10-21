package cnab

import (
	"context"
	"testing"

	"github.com/cnabio/cnab-go/bundle"
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
