package installer

import (
	"encoding/json"
	"fmt"

	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/types/registry"

	"github.com/sntns/nemo/internal/decode"
	"github.com/sntns/nemo/pkg/cnab"
)

// RegistryAuth holds the credentials of registries, by domain, as the
// runtime hands them in nemo-registry-auth.
type RegistryAuth map[string]registry.AuthConfig

// For returns the encoded credentials of the image's registry, as the
// Engine API takes them, or nothing when there are none.
func (auth RegistryAuth) For(image string) (string, error) {
	if len(auth) == 0 {
		return "", nil
	}
	named, err := cnab.ParseOCIReference(image)
	if err != nil {
		return "", fmt.Errorf("image %s: %w", image, err)
	}
	config, ok := auth[named.Registry()]
	if !ok {
		return "", nil
	}
	return authconfig.Encode(config)
}

// registryAuthOf reads the nemo-registry-auth parameter. Its value never
// appears in an error.
func registryAuthOf(parameters map[string]any) (RegistryAuth, error) {
	text, err := parameter[string](parameters, cnab.ParameterRegistryAuth)
	if err != nil || text == "" {
		return nil, err
	}
	var auth RegistryAuth
	if err := decode.Strict(json.RawMessage(text), &auth); err != nil {
		return nil, fmt.Errorf("parameter %s is not a map of registry credentials", cnab.ParameterRegistryAuth)
	}
	return auth, nil
}
