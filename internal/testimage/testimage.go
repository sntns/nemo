// Package testimage writes images for tests, without a registry.
package testimage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// Write writes an OCI layout holding one image, as the sntns pipeline
// builds one: a layer, a config, and the manifest index.json names. It
// returns the manifest's digest.
func Write(t *testing.T, directory string) digest.Digest {
	t.Helper()
	blob := func(data []byte) ocispec.Descriptor {
		dgst := digest.FromBytes(data)
		path := filepath.Join(directory, ocispec.ImageBlobsDir, dgst.Algorithm().String(), dgst.Encoded())
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, data, 0o644))
		return ocispec.Descriptor{Digest: dgst, Size: int64(len(data))}
	}
	layer := blob([]byte("layer of " + directory))
	layer.MediaType = ocispec.MediaTypeImageLayer
	config := blob([]byte(`{"architecture":"amd64","os":"linux"}`))
	config.MediaType = ocispec.MediaTypeImageConfig
	manifest := ocispec.Manifest{MediaType: ocispec.MediaTypeImageManifest, Config: config, Layers: []ocispec.Descriptor{layer}}
	manifest.SchemaVersion = 2
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	descriptor := blob(data)
	descriptor.MediaType = ocispec.MediaTypeImageManifest

	index := ocispec.Index{Manifests: []ocispec.Descriptor{descriptor}}
	index.SchemaVersion = 2
	data, err = json.Marshal(index)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, ocispec.ImageIndexFile), data, 0o644))
	return descriptor.Digest
}
