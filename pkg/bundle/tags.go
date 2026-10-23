package bundle

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/sntns/nemo/pkg/cnab"
)

// RegistryTags lists the tags of a repository in its registry, with the
// credentials of docker login: what cnab.Resolver picks a dependency's
// version among.
type RegistryTags struct{}

var _ cnab.TagLister = RegistryTags{}

func (RegistryTags) ListTags(ctx context.Context, repository cnab.OCIReference) ([]string, error) {
	named, err := name.NewRepository(repository.Named.Name())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", repository, err)
	}
	return remote.List(named, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain))
}
