package bundle

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cnabio/cnab-to-oci/remotes"
	containerdremotes "github.com/containerd/containerd/v2/core/remotes"
	"github.com/docker/cli/cli/config"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/transfer"
)

// Transports of a location.
const (
	// Registry is docker://REFERENCE, a reference in a registry.
	Registry = "docker"
	// Layout is oci:PATH[:REFERENCE], an OCI image layout directory.
	Layout = "oci"
)

// layoutDomain names the repository of a layout for the code that wants a
// registry reference; it reaches no registry.
const layoutDomain = "layout.nemo.local"

// Location is where an image or a bundle is: in a registry, or in an OCI
// image layout directory, as the sntns pipeline builds them.
type Location struct {
	Transport string
	// Reference is the registry reference, or the reference name in the
	// layout, empty when the layout holds a single manifest.
	Reference string
	// Path is the layout directory.
	Path string
}

// ParseLocation parses docker://REFERENCE or oci:PATH[:REFERENCE].
func ParseLocation(text string) (Location, error) {
	if reference, ok := strings.CutPrefix(text, "docker://"); ok && reference != "" {
		return Location{Transport: Registry, Reference: reference}, nil
	}
	if rest, ok := strings.CutPrefix(text, "oci:"); ok && rest != "" {
		path, reference, _ := strings.Cut(rest, ":")
		return Location{Transport: Layout, Path: path, Reference: reference}, nil
	}
	return Location{}, fmt.Errorf("location %q: expected docker://REFERENCE or oci:PATH[:REFERENCE]", text)
}

func (location Location) String() string {
	if location.Transport == Registry {
		return "docker://" + location.Reference
	}
	if location.Reference == "" {
		return "oci:" + location.Path
	}
	return "oci:" + location.Path + ":" + location.Reference
}

// resolver returns what reaches the location: the registry, with the
// credentials of docker login, or the layout directory.
func (location Location) resolver() containerdremotes.Resolver {
	if location.Transport == Registry {
		return remotes.CreateResolver(config.LoadDefaultConfigFile(os.Stderr))
	}
	return newLayout(location.Path)
}

// named returns the location as a registry reference, tagged with tag when
// it has none. A layout's repository is named after its directory.
func (location Location) named(tag string) (cnab.OCIReference, error) {
	text := location.Reference
	if location.Transport == Layout {
		repository := strings.ToLower(strings.TrimSuffix(filepath.Base(location.Path), ".oci"))
		text = layoutDomain + "/" + repository
		if location.Reference != "" {
			text += ":" + location.Reference
		}
	}
	named, err := cnab.ParseOCIReference(text)
	if err != nil {
		return cnab.OCIReference{}, fmt.Errorf("%s: %w", location, err)
	}
	if !named.HasTag() && tag != "" {
		return named.WithTag(tag)
	}
	return named, nil
}

// Resolve returns the descriptor of the manifest, or of the index, the location names.
func (location Location) Resolve(ctx context.Context) (ocispec.Descriptor, error) {
	named, err := location.named("")
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	_, descriptor, err := location.resolver().Resolve(ctx, named.Canonical())
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("%s: %w", location, err)
	}
	return descriptor, nil
}

// Image is the image of the location, as the bundle names it: read where
// the location is, its digest is the one found there.
func (location Location) Image(name string) transfer.Image {
	return transfer.Image{Name: name, Source: func(ctx context.Context) (containerdremotes.Fetcher, ocispec.Descriptor, error) {
		descriptor, err := location.Resolve(ctx)
		if err != nil {
			return nil, ocispec.Descriptor{}, err
		}
		named, err := location.named("")
		if err != nil {
			return nil, ocispec.Descriptor{}, err
		}
		fetcher, err := location.resolver().Fetcher(ctx, named.Canonical())
		return fetcher, descriptor, err
	}}
}

// ImageName is the name the image of the location runs as, when nothing
// relocates it: its reference, or a layout's directory name tagged with its
// reference, latest when it has none.
func (location Location) ImageName() string {
	if location.Transport == Registry {
		return location.Reference
	}
	return strings.TrimSuffix(filepath.Base(location.Path), ".oci") + ":" + cmp.Or(location.Reference, "latest")
}
