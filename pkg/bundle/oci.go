package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/cnabio/cnab-to-oci/relocation"
	"github.com/cnabio/cnab-to-oci/remotes"
	containerdremotes "github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/errdefs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/transfer"
)

// Push writes the bundle and its images to destination, the way cnab-to-oci
// puts a bundle in a registry, which Porter reads: each image copied into
// the bundle's repository by its digest, then the bundle, as an index of
// them tagged with the bundle's version unless destination names a tag.
// sources are the images of the bundle, the invocation image's included,
// read where they are.
func Push(ctx context.Context, built cnab.ExtendedBundle, sources []transfer.Image, destination Location) (ocispec.Descriptor, error) {
	target, err := destination.named(built.Version)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	resolver := destination.resolver()
	repository := target.WithoutVersion()

	byName := map[string]transfer.Image{}
	for _, source := range sources {
		byName[source.Name] = source
	}
	relocationMap := relocation.ImageRelocationMap{}
	for _, image := range baseImages(built) {
		source, ok := byName[image.Image]
		if !ok {
			return ocispec.Descriptor{}, fmt.Errorf("image %s has no location to copy it from", image.Image)
		}
		fetcher, descriptor, err := source.Source(ctx)
		if err != nil {
			return ocispec.Descriptor{}, fmt.Errorf("image %s: %w", image.Image, err)
		}
		if descriptor.Digest.String() != image.Digest {
			return ocispec.Descriptor{}, fmt.Errorf("image %s is %s where it is read, the bundle was built with %s: build it again", image.Image, descriptor.Digest, image.Digest)
		}
		if err := copyGraph(ctx, fetcher, resolver, repository, descriptor); err != nil {
			return ocispec.Descriptor{}, fmt.Errorf("copying image %s to %s: %w", image.Image, destination, err)
		}
		relocated, err := repository.WithDigest(descriptor.Digest)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		relocationMap[image.Image] = relocated.String()
	}

	descriptor, err := remotes.Push(ctx, &built.Bundle, relocationMap, target.Named, resolver, true)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("pushing the bundle to %s: %w", destination, err)
	}
	return descriptor, nil
}

// Pull reads the bundle at the location. From a registry, its relocation
// map says where its images are; a layout's images are on no registry, so
// it has none.
func Pull(ctx context.Context, location Location) (cnab.BundleReference, error) {
	named, err := location.named("")
	if err != nil {
		return cnab.BundleReference{}, err
	}
	pulled, relocationMap, dgst, err := remotes.Pull(ctx, named.Named, location.resolver())
	if err != nil {
		return cnab.BundleReference{}, fmt.Errorf("pulling the bundle at %s: %w", location, err)
	}
	extended, err := cnab.NewBundle(*pulled)
	if err != nil {
		return cnab.BundleReference{}, fmt.Errorf("the bundle at %s: %w", location, err)
	}
	if location.Transport == Layout {
		relocationMap = nil
	}
	return cnab.BundleReference{Reference: named, Digest: dgst, Definition: extended, RelocationMap: relocationMap}, nil
}

// Copy copies the bundle at source and its images to destination, as
// porter copy does: from an OCI layout to a registry, for one. The bundle
// is tagged with its version unless destination names a tag.
func Copy(ctx context.Context, source, destination Location) (ocispec.Descriptor, error) {
	pulled, err := Pull(ctx, source)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return Push(ctx, pulled.Definition, ImagesOf(pulled.Definition, pulled.RelocationMap, source.String()), destination)
}

// copyGraph copies the manifest or the index, and what it references, to
// the repository, children first: a registry refuses a manifest whose
// blobs it does not have. What the destination has already is skipped.
func copyGraph(ctx context.Context, fetcher containerdremotes.Fetcher, destination containerdremotes.Resolver, repository cnab.OCIReference, descriptor ocispec.Descriptor) error {
	reader, err := fetcher.Fetch(ctx, descriptor)
	if err != nil {
		return err
	}
	defer reader.Close()
	if !isManifest(descriptor.MediaType) {
		return push(ctx, destination, repository.Canonical(), descriptor, reader)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	var manifest struct {
		Manifests []ocispec.Descriptor `json:"manifests"`
		Config    *ocispec.Descriptor  `json:"config"`
		Layers    []ocispec.Descriptor `json:"layers"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("%s: %w", descriptor.Digest, err)
	}
	children := append(manifest.Manifests, manifest.Layers...)
	if manifest.Config != nil {
		children = append(children, *manifest.Config)
	}
	for _, child := range children {
		if err := copyGraph(ctx, fetcher, destination, repository, child); err != nil {
			return err
		}
	}
	// A manifest is pushed by its digest: under a tag, a registry would name it by the tag.
	byDigest, err := repository.WithDigest(descriptor.Digest)
	if err != nil {
		return err
	}
	return push(ctx, destination, byDigest.Canonical(), descriptor, bytes.NewReader(data))
}

func push(ctx context.Context, destination containerdremotes.Resolver, ref string, descriptor ocispec.Descriptor, content io.Reader) error {
	pusher, err := destination.Pusher(ctx, ref)
	if err != nil {
		return err
	}
	writer, err := pusher.Push(ctx, descriptor)
	if errdefs.IsAlreadyExists(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer writer.Close()
	if _, err := io.Copy(writer, content); err != nil {
		return err
	}
	if err := writer.Commit(ctx, descriptor.Size, descriptor.Digest); err != nil && !errdefs.IsAlreadyExists(err) {
		return err
	}
	return nil
}
