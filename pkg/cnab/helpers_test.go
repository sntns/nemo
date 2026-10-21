package cnab

import (
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/stretchr/testify/require"
)

func ReadTestBundle(t *testing.T, path string) ExtendedBundle {
	t.Helper()
	bundle, err := LoadBundle(path)
	require.NoError(t, err)
	return bundle
}

// testBundle is the smallest valid nemo bundle, changed by edit.
func testBundle(t *testing.T, edit func(cnabBundle *bundle.Bundle)) ExtendedBundle {
	t.Helper()
	cnabBundle := bundle.Bundle{
		SchemaVersion: "1.2.0",
		Name:          "nginx",
		Version:       "1.0.0",
		InvocationImages: []bundle.InvocationImage{
			{BaseImage: bundle.BaseImage{ImageType: "docker", Image: "gill:dev"}},
		},
		RequiredExtensions: []string{DockerExtension.Key},
		Custom:             map[string]any{DockerExtension.Key: map[string]any{}},
	}
	if edit != nil {
		edit(&cnabBundle)
	}
	extendedBundle, err := NewBundle(cnabBundle)
	require.NoError(t, err)
	return extendedBundle
}
