package cnab

import (
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/bundle/definition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadBundle(t *testing.T) {
	t.Parallel()

	bundle := ReadTestBundle(t, "testdata/bundle.json")

	assert.Equal(t, &Docker{}, bundle.Docker)

	require.NotNil(t, bundle.Dependencies)
	assert.Equal(t, []string{"storage", "mysql"}, bundle.Dependencies.Sequence)
	assert.Equal(t, Dependency{
		Bundle:  "somecloud/mysql",
		Version: &DependencyVersion{Ranges: []string{"5.7.x"}, AllowPrereleases: true},
	}, bundle.Dependencies.Requires["mysql"])

	require.NotNil(t, bundle.Compose)
	assert.Equal(t, "compose.yaml", bundle.Compose.Documents[0].Name)
}

func TestNewBundle(t *testing.T) {
	t.Parallel()

	t.Run("without extensions", func(t *testing.T) {
		t.Parallel()
		extendedBundle := testBundle(t, func(bundle *bundle.Bundle) {
			bundle.RequiredExtensions = nil
			bundle.Custom = nil
		})
		assert.Nil(t, extendedBundle.Docker)
		assert.Nil(t, extendedBundle.Dependencies)
		assert.Nil(t, extendedBundle.Compose)
	})

	t.Run("unsupported schema version", func(t *testing.T) {
		t.Parallel()
		bundle := testBundle(t, nil).Bundle
		bundle.SchemaVersion = "2.0.0"
		_, err := NewBundle(bundle)
		require.ErrorContains(t, err, "unsupported bundle schema version 2.0.0")
	})

	t.Run("required extension without its entry", func(t *testing.T) {
		t.Parallel()
		bundle := testBundle(t, nil).Bundle
		bundle.Custom = nil
		_, err := NewBundle(bundle)
		require.ErrorContains(t, err, "io.cnab.docker")
	})

	t.Run("invalid extension", func(t *testing.T) {
		t.Parallel()
		bundle := testBundle(t, nil).Bundle
		bundle.Custom[DependenciesExtension.Key] = map[string]any{"requires": map[string]any{}}
		_, err := NewBundle(bundle)
		require.ErrorContains(t, err, "invalid bundle: io.cnab.dependencies:")
	})
}

func TestExtendedBundle_IsFileType(t *testing.T) {
	stringDefinition := &definition.Schema{
		Type: "string",
	}
	fileDefinition := &definition.Schema{
		Type:            "string",
		ContentEncoding: "base64",
	}
	bundle := testBundle(t, nil)

	assert.False(t, bundle.IsFileType(stringDefinition), "strings should not be flagged as files")
	assert.True(t, bundle.IsFileType(fileDefinition), "strings+base64 should be categorized as files")
}

func TestExtendedBundle_IsSensitiveParameter(t *testing.T) {
	sensitive := true
	extendedBundle := testBundle(t, func(cnabBundle *bundle.Bundle) {
		cnabBundle.Definitions = definition.Definitions{
			"foo":   &definition.Schema{Type: "string", WriteOnly: &sensitive},
			"debug": &definition.Schema{Type: "string"},
		}
		cnabBundle.Parameters = map[string]bundle.Parameter{
			"foo":   {Definition: "foo", Destination: &bundle.Location{EnvironmentVariable: "FOO"}},
			"debug": {Definition: "debug", Destination: &bundle.Location{EnvironmentVariable: "DEBUG"}},
		}
	})

	assert.False(t, extendedBundle.IsSensitiveParameter("bar"), "param does not exist")
	assert.False(t, extendedBundle.IsSensitiveParameter("debug"), "is not sensitive")
	assert.True(t, extendedBundle.IsSensitiveParameter("foo"), "is sensitive")
}

func TestExtendedBundle_GetReferencedRegistries(t *testing.T) {
	images := func(invocation string) ExtendedBundle {
		return testBundle(t, func(cnabBundle *bundle.Bundle) {
			cnabBundle.InvocationImages = []bundle.InvocationImage{
				{BaseImage: bundle.BaseImage{ImageType: "docker", Image: invocation}},
			}
			cnabBundle.Images = map[string]bundle.Image{
				"nginx": {BaseImage: bundle.BaseImage{ImageType: "docker", Image: "quay.io/library/nginx:latest"}},
				"redis": {BaseImage: bundle.BaseImage{ImageType: "docker", Image: "quay.io/library/redis:latest"}},
				"helm":  {BaseImage: bundle.BaseImage{ImageType: "docker", Image: "ghcr.io/library/helm:latest"}},
			}
		})
	}

	t.Run("bundle image in different registry", func(t *testing.T) {
		registries, err := images("docker.io/example/mybuns:abc123").GetReferencedRegistries()
		require.NoError(t, err, "GetReferencedRegistries failed")
		require.Equal(t, []string{"docker.io", "ghcr.io", "quay.io"}, registries)
	})

	t.Run("bundle image in same registry", func(t *testing.T) {
		registries, err := images("ghcr.io/example/mybuns:abc123").GetReferencedRegistries()
		require.NoError(t, err, "GetReferencedRegistries failed")
		require.Equal(t, []string{"ghcr.io", "quay.io"}, registries, "no duplicate registry entries")
	})
}
