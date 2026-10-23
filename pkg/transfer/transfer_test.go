package transfer

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/progress"

	"github.com/sntns/nemo/internal/testimage"
)

// fakeDocker is a daemon that keeps the names of what it has and of what
// it was given to load, with the files of each archive.
type fakeDocker struct {
	client.APIClient
	has    map[string]bool
	loaded map[string]map[string][]byte
}

func (fake *fakeDocker) Info(ctx context.Context, options client.InfoOptions) (client.SystemInfoResult, error) {
	return client.SystemInfoResult{Info: system.Info{OSType: "linux", Architecture: "x86_64"}}, nil
}

func (fake *fakeDocker) ImageInspect(ctx context.Context, name string, options ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	if fake.has[name] {
		return client.ImageInspectResult{}, nil
	}
	return client.ImageInspectResult{}, errdefs.ErrNotFound
}

func (fake *fakeDocker) ImageLoad(ctx context.Context, input io.Reader, options ...client.ImageLoadOption) (client.ImageLoadResult, error) {
	files := map[string][]byte{}
	archive := tar.NewReader(input)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if files[header.Name], err = io.ReadAll(archive); err != nil {
			return nil, err
		}
	}
	var manifests []struct{ RepoTags []string }
	if err := json.Unmarshal(files["manifest.json"], &manifests); err != nil {
		return nil, err
	}
	fake.loaded[manifests[0].RepoTags[0]] = files
	return io.NopCloser(strings.NewReader("")), nil
}

// layoutImage is an image of an OCI layout, read as the runtime reads one.
func layoutImage(t *testing.T, name, directory string) Image {
	t.Helper()
	manifest := testimage.Write(t, directory)
	return Image{Name: name, Digest: manifest, Source: func(ctx context.Context) (remotes.Fetcher, ocispec.Descriptor, error) {
		data, err := os.ReadFile(filepath.Join(directory, ocispec.ImageBlobsDir, "sha256", manifest.Encoded()))
		if err != nil {
			return nil, ocispec.Descriptor{}, err
		}
		fetcher := remotes.FetcherFunc(func(ctx context.Context, descriptor ocispec.Descriptor) (io.ReadCloser, error) {
			return os.Open(filepath.Join(directory, ocispec.ImageBlobsDir, "sha256", descriptor.Digest.Encoded()))
		})
		return fetcher, ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: manifest, Size: int64(len(data))}, nil
	}}
}

func TestLoad(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	web := layoutImage(t, "sntns-cloud-service-faas-domain:latest", filepath.Join(directory, "web.oci"))
	cache := layoutImage(t, "redis:7", filepath.Join(directory, "cache.oci"))
	cacheName, err := LoadedName(cache.Name, cache.Digest)
	require.NoError(t, err)
	fake := &fakeDocker{
		has:    map[string]bool{"gill:dev": true, cacheName: true},
		loaded: map[string]map[string][]byte{},
	}
	installer := Image{Name: "gill:dev", Source: func(ctx context.Context) (remotes.Fetcher, ocispec.Descriptor, error) {
		t.Fatal("an image the daemon has by name, with no digest, is not read")
		return nil, ocispec.Descriptor{}, nil
	}}

	var counts []string
	events := progress.SinkFunc(func(event progress.Event) {
		counts = append(counts, fmt.Sprintf("%d/%d %s", event.Current, event.Total, event.State))
	})
	relocated, err := Load(context.Background(), fake, []Image{installer, web, cache}, events)
	require.NoError(t, err)
	assert.Equal(t, []string{"0/3 working", "1/3 working", "2/3 working", "3/3 done"}, counts, "each image counted")

	webName := "sntns-cloud-service-faas-domain:sha256-" + web.Digest.Encoded()
	assert.Equal(t, webName, relocated[web.Name], "named after its digest")
	assert.Equal(t, cacheName, relocated[cache.Name])
	assert.NotContains(t, relocated, installer.Name, "runs as it is")
	assert.NotContains(t, fake.loaded, cacheName, "the daemon had it")

	files := fake.loaded[webName]
	require.NotNil(t, files, "loaded")
	var index ocispec.Index
	require.NoError(t, json.Unmarshal(files[ocispec.ImageIndexFile], &index))
	assert.Equal(t, "docker.io/library/"+webName, index.Manifests[0].Annotations[images.AnnotationImageName], "the containerd store reads this name")
	assert.Contains(t, files, filepath.Join(ocispec.ImageBlobsDir, "sha256", web.Digest.Encoded()))
	assert.Equal(t, digest.FromBytes(files[filepath.Join(ocispec.ImageBlobsDir, "sha256", web.Digest.Encoded())]), web.Digest, "blobs as they are")
}
