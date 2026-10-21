package cnab

import (
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtension_Read(t *testing.T) {
	t.Parallel()

	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		_, err := DockerExtension.Read(bundle.Bundle{})
		require.ErrorIs(t, err, ErrExtensionNotFound)
	})

	t.Run("against its schema", func(t *testing.T) {
		t.Parallel()
		cnabBundle := bundle.Bundle{Custom: map[string]any{DockerExtension.Key: map[string]any{"privileged": "yes"}}}
		_, err := DockerExtension.Read(cnabBundle)
		require.ErrorContains(t, err, "io.cnab.docker:")
	})

	t.Run("every extension has its schema", func(t *testing.T) {
		t.Parallel()
		all, err := schemas()
		require.NoError(t, err)
		for _, schema := range []string{DockerExtension.Schema, DependenciesExtension.Schema, ComposeExtension.Schema} {
			assert.Contains(t, all, schema)
		}
	})
}

func TestExtension_Write(t *testing.T) {
	t.Parallel()

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()
		var cnabBundle bundle.Bundle
		require.NoError(t, DockerExtension.Write(&cnabBundle, Docker{Privileged: true}))
		got, err := DockerExtension.Read(cnabBundle)
		require.NoError(t, err)
		assert.Equal(t, Docker{Privileged: true}, got)
	})

	t.Run("against its schema", func(t *testing.T) {
		t.Parallel()
		var cnabBundle bundle.Bundle
		err := ComposeExtension.Write(&cnabBundle, Compose{SchemaVersion: "1.0.0"})
		require.ErrorContains(t, err, "io.nemo.compose:")
		assert.NotContains(t, cnabBundle.Custom, ComposeExtension.Key)
	})
}

func TestExtension_Matches(t *testing.T) {
	t.Parallel()

	assert.True(t, DockerExtension.Matches("io.cnab.docker"))
	assert.True(t, DockerExtension.Matches("docker"), "bundles in the wild require it by its shorthand")
	assert.False(t, DockerExtension.Matches("io.cnab"))
	assert.False(t, ComposeExtension.Matches(""), "an extension without shorthand never matches an empty name")
}

func TestExtendedBundle_CheckRequiredExtensions(t *testing.T) {
	t.Parallel()

	t.Run("supported", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, ReadTestBundle(t, "testdata/bundle.json").CheckRequiredExtensions())
	})

	t.Run("unsupported", func(t *testing.T) {
		t.Parallel()
		extendedBundle := testBundle(t, func(bundle *bundle.Bundle) {
			bundle.RequiredExtensions = append(bundle.RequiredExtensions, "donuts")
			bundle.Custom["donuts"] = map[string]any{}
		})
		require.EqualError(t, extendedBundle.CheckRequiredExtensions(), "unsupported required extension: donuts")
	})

	t.Run("installer extension", func(t *testing.T) {
		t.Parallel()
		extendedBundle := testBundle(t, func(bundle *bundle.Bundle) {
			bundle.RequiredExtensions = append(bundle.RequiredExtensions, ComposeExtension.Key)
			bundle.Custom[ComposeExtension.Key] = map[string]any{"schemaVersion": "1.0.0", "compose": []any{map[string]any{"config": map[string]any{}}}}
		})
		require.ErrorContains(t, extendedBundle.CheckRequiredExtensions(), "io.nemo.compose must not be listed in requiredExtensions")
	})
}
