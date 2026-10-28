// Package transfer carries a bundle's images into the target's daemon,
// with no registry in between.
package transfer

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"

	"github.com/cnabio/cnab-to-oci/relocation"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/jsonmessage"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/progress"
)

// Image is an image of a bundle to carry.
type Image struct {
	// Name is the name the bundle gives it.
	Name string
	// Digest is its digest, empty when the bundle does not give one.
	Digest digest.Digest
	// Source reaches where the image is, only when it must be read.
	Source func(ctx context.Context) (remotes.Fetcher, ocispec.Descriptor, error)
}

// LoadedName is the name an image runs as once loaded: its repository,
// tagged after its digest, so that what runs changes with the content,
// without a registry to resolve the digest.
func LoadedName(name string, dgst digest.Digest) (string, error) {
	named, err := cnab.ParseOCIReference(name)
	if err != nil {
		return "", fmt.Errorf("image %s: %w", name, err)
	}
	tagged, err := named.WithoutVersion().WithTag(dgst.Algorithm().String() + "-" + dgst.Encoded())
	if err != nil {
		return "", fmt.Errorf("image %s: %w", name, err)
	}
	return tagged.String(), nil
}

// Load loads into the daemon, for its platform only, each image it does
// not have yet, and returns where each one is now: its loaded name. An
// image the bundle gives no digest for, and that the daemon has under its
// name, runs as it is. Events count the images done.
func Load(ctx context.Context, api client.APIClient, images []Image, events progress.Sink) (relocation.ImageRelocationMap, error) {
	info, err := api.Info(ctx, client.InfoOptions{})
	if err != nil {
		return nil, fmt.Errorf("reaching the target's Docker: %w", err)
	}
	platform := platforms.Normalize(ocispec.Platform{OS: info.Info.OSType, Architecture: info.Info.Architecture})

	relocationMap := relocation.ImageRelocationMap{}
	total := int64(len(images))
	for done, image := range images {
		events.Emit(progress.Event{Group: progress.Images, Item: image.Name, State: progress.Working, Current: int64(done), Total: total})
		loaded, err := carry(ctx, api, image, platform)
		if err != nil {
			return nil, err
		}
		if loaded != "" {
			relocationMap[image.Name] = loaded
		}
	}
	events.Emit(progress.Event{Group: progress.Images, State: progress.Done, Current: total, Total: total})
	return relocationMap, nil
}

// carry loads the image unless the daemon has it, and returns its loaded
// name: none for an image with no digest that the daemon has by name. Its
// source is read once at most, for its digest or for its content.
func carry(ctx context.Context, api client.APIClient, image Image, platform ocispec.Platform) (string, error) {
	var fetcher remotes.Fetcher
	var descriptor ocispec.Descriptor
	read := func() (err error) {
		if fetcher == nil {
			fetcher, descriptor, err = image.Source(ctx)
		}
		return err
	}

	dgst := image.Digest
	if dgst == "" {
		if has(ctx, api, image.Name) {
			return "", nil
		}
		if err := read(); err != nil {
			return "", err
		}
		dgst = descriptor.Digest
	}
	loaded, err := LoadedName(image.Name, dgst)
	if err != nil {
		return "", err
	}
	if has(ctx, api, loaded) {
		return loaded, nil
	}
	if err := read(); err != nil {
		return "", err
	}
	if err := load(ctx, api, fetcher, descriptor, platform, loaded); err != nil {
		return "", fmt.Errorf("loading image %s into the target's Docker: %w", image.Name, err)
	}
	return loaded, nil
}

func has(ctx context.Context, api client.APIClient, name string) bool {
	_, err := api.ImageInspect(ctx, name)
	return err == nil
}

// load streams the image of the platform to the daemon, as docker save
// writes it: an OCI layout the containerd image store reads, with the
// manifest.json the classic image store reads.
func load(ctx context.Context, api client.APIClient, fetcher remotes.Fetcher, descriptor ocispec.Descriptor, platform ocispec.Platform, name string) error {
	manifestDescriptor, manifest, err := manifestOf(ctx, fetcher, descriptor, platform)
	if err != nil {
		return err
	}

	reader, writer := io.Pipe()
	go func() {
		writer.CloseWithError(writeArchive(ctx, writer, fetcher, manifestDescriptor, manifest, name))
	}()
	response, err := api.ImageLoad(ctx, reader, client.ImageLoadWithQuiet(true))
	if err != nil {
		reader.CloseWithError(err)
		return err
	}
	defer response.Close()
	return jsonmessage.DisplayJSONMessagesStream(response, io.Discard, 0, false, nil)
}

// manifestOf returns the manifest of the platform: the descriptor's own, or
// the one an index has for it.
func manifestOf(ctx context.Context, fetcher remotes.Fetcher, descriptor ocispec.Descriptor, platform ocispec.Platform) (ocispec.Descriptor, ocispec.Manifest, error) {
	data, err := fetch(ctx, fetcher, descriptor)
	if err != nil {
		return descriptor, ocispec.Manifest{}, err
	}
	if images.IsIndexType(descriptor.MediaType) {
		var index ocispec.Index
		if err := json.Unmarshal(data, &index); err != nil {
			return descriptor, ocispec.Manifest{}, err
		}
		matcher := platforms.Only(platform)
		for _, candidate := range index.Manifests {
			if candidate.Platform != nil && matcher.Match(*candidate.Platform) {
				return manifestOf(ctx, fetcher, candidate, platform)
			}
		}
		return descriptor, ocispec.Manifest{}, fmt.Errorf("%s has no image for %s: %w", descriptor.Digest, platforms.Format(platform), errdefs.ErrNotFound)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return descriptor, ocispec.Manifest{}, err
	}
	return descriptor, manifest, nil
}

// writeArchive writes the image's blobs, then its index.json named after
// name, and its manifest.json.
func writeArchive(ctx context.Context, output io.Writer, fetcher remotes.Fetcher, manifestDescriptor ocispec.Descriptor, manifest ocispec.Manifest, name string) error {
	archive := tar.NewWriter(output)
	blobPath := func(dgst digest.Digest) string {
		return path.Join(ocispec.ImageBlobsDir, dgst.Algorithm().String(), dgst.Encoded())
	}
	file := func(name string, data []byte) error {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			return err
		}
		_, err := archive.Write(data)
		return err
	}

	for _, blob := range append([]ocispec.Descriptor{manifest.Config, manifestDescriptor}, manifest.Layers...) {
		reader, err := fetcher.Fetch(ctx, blob)
		if err != nil {
			return err
		}
		err = archive.WriteHeader(&tar.Header{Name: blobPath(blob.Digest), Mode: 0o644, Size: blob.Size})
		if err == nil {
			_, err = io.CopyN(archive, reader, blob.Size)
		}
		reader.Close()
		if err != nil {
			return err
		}
	}

	named, err := cnab.ParseOCIReference(name)
	if err != nil {
		return err
	}
	root := ocispec.Descriptor{MediaType: manifestDescriptor.MediaType, Digest: manifestDescriptor.Digest, Size: manifestDescriptor.Size, Annotations: map[string]string{
		images.AnnotationImageName: named.Canonical(),
		ocispec.AnnotationRefName:  named.Tag(),
	}}
	index := ocispec.Index{MediaType: ocispec.MediaTypeImageIndex, Manifests: []ocispec.Descriptor{root}}
	index.SchemaVersion = 2
	indexData, err := json.Marshal(index)
	if err != nil {
		return err
	}
	layers := make([]string, len(manifest.Layers))
	for i, layer := range manifest.Layers {
		layers[i] = blobPath(layer.Digest)
	}
	manifestData, err := json.Marshal([]map[string]any{{"Config": blobPath(manifest.Config.Digest), "RepoTags": []string{name}, "Layers": layers}})
	if err != nil {
		return err
	}
	for name, data := range map[string][]byte{
		ocispec.ImageLayoutFile: []byte(`{"imageLayoutVersion":"` + ocispec.ImageLayoutVersion + `"}`),
		ocispec.ImageIndexFile:  indexData,
		"manifest.json":         manifestData,
	} {
		if err := file(name, data); err != nil {
			return err
		}
	}
	return archive.Close()
}

func fetch(ctx context.Context, fetcher remotes.Fetcher, descriptor ocispec.Descriptor) ([]byte, error) {
	reader, err := fetcher.Fetch(ctx, descriptor)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
