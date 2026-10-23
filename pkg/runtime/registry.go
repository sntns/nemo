package runtime

import (
	"encoding/json"

	"github.com/cnabio/cnab-to-oci/relocation"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/moby/moby/api/types/registry"

	"github.com/sntns/nemo/pkg/cnab"
)

// dockerHubCredentials is the key docker login keeps Docker Hub's credentials under.
const dockerHubCredentials = "https://index.docker.io/v1/"

// registryAuthOf returns, as JSON by domain, the credentials docker login
// keeps for the registries the bundle's images come from: where they were
// relocated, else where the bundle names them. The installer pulls with
// them, and hands them to Swarm for its nodes. Empty when there are none.
func registryAuthOf(bundle cnab.ExtendedBundle, relocationMap relocation.ImageRelocationMap, credentials *configfile.ConfigFile) (string, error) {
	images := []string{bundle.InvocationImages[0].Image}
	for _, image := range bundle.Images {
		images = append(images, image.Image)
	}

	auth := map[string]registry.AuthConfig{}
	for _, image := range images {
		if relocated, ok := relocationMap[image]; ok {
			image = relocated
		}
		credentials, found, err := registryCredentials(image, credentials)
		if err != nil {
			return "", err
		}
		if found {
			named, _ := cnab.ParseOCIReference(image)
			auth[named.Registry()] = credentials
		}
	}
	if len(auth) == 0 {
		return "", nil
	}
	data, err := json.Marshal(auth)
	return string(data), err
}

// registryCredentials returns what docker login keeps for the registry of
// the image, if anything.
func registryCredentials(image string, credentials *configfile.ConfigFile) (registry.AuthConfig, bool, error) {
	named, err := cnab.ParseOCIReference(image)
	if err != nil {
		return registry.AuthConfig{}, false, nil
	}
	key := named.Registry()
	if key == "docker.io" {
		key = dockerHubCredentials
	}
	found, err := credentials.GetAuthConfig(key)
	if err != nil {
		return registry.AuthConfig{}, false, err
	}
	if found.Username == "" && found.IdentityToken == "" && found.RegistryToken == "" {
		return registry.AuthConfig{}, false, nil
	}
	return registry.AuthConfig{Username: found.Username, Password: found.Password, IdentityToken: found.IdentityToken, RegistryToken: found.RegistryToken, ServerAddress: key}, true, nil
}
