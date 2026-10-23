package bundle

import (
	"context"
	"fmt"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-to-oci/relocation"
	containerdremotes "github.com/containerd/containerd/v2/core/remotes"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/transfer"
)

// ImagesOf returns the images of the bundle, its invocation image's
// included, with where to read them: in the OCI layout the bundle was
// pulled from, else in a registry, where they were relocated or where the
// bundle names them. source is where the bundle came from: a bundle.json,
// or a location.
func ImagesOf(definition cnab.ExtendedBundle, relocationMap relocation.ImageRelocationMap, source string) []transfer.Image {
	location, err := ParseLocation(source)
	fromLayout := err == nil && location.Transport == Layout

	bundleImages := baseImages(definition)
	images := make([]transfer.Image, len(bundleImages))
	for i, image := range bundleImages {
		images[i] = transfer.Image{Name: image.Image, Digest: digest.Digest(image.Digest)}
		if fromLayout {
			images[i].Source = func(ctx context.Context) (containerdremotes.Fetcher, ocispec.Descriptor, error) {
				if image.Digest == "" {
					return nil, ocispec.Descriptor{}, fmt.Errorf("image %s has no digest to find it in %s", image.Image, location)
				}
				fetcher, err := newLayout(location.Path).Fetcher(ctx, "")
				return fetcher, ocispec.Descriptor{MediaType: image.MediaType, Digest: digest.Digest(image.Digest), Size: int64(image.Size)}, err
			}
			continue
		}
		reference := image.Image
		if relocated, ok := relocationMap[image.Image]; ok {
			reference = relocated
		}
		images[i].Source = Location{Transport: Registry, Reference: reference}.Image(image.Image).Source
	}
	return images
}

// baseImages are the images of the bundle, its invocation image first.
func baseImages(definition cnab.ExtendedBundle) []bundle.BaseImage {
	images := []bundle.BaseImage{definition.InvocationImages[0].BaseImage}
	for _, image := range definition.Images {
		images = append(images, image.BaseImage)
	}
	return images
}
