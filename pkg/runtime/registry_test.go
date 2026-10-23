package runtime

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-to-oci/relocation"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/moby/moby/api/types/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryAuthOf(t *testing.T) {
	t.Parallel()
	credentials := configfile.New("")
	encoded := base64.StdEncoding.EncodeToString([]byte("bot:s3cret"))
	require.NoError(t, credentials.LoadFromReader(strings.NewReader(`{"auths":{"ghcr.io":{"auth":"`+encoded+`"},"https://index.docker.io/v1/":{"auth":"`+encoded+`"}}}`)))

	extended := testBundle(t)
	extended.Images = map[string]bundle.Image{
		"web":   {BaseImage: bundle.BaseImage{Image: "web:latest"}},
		"cache": {BaseImage: bundle.BaseImage{Image: "quay.io/redis:7"}},
	}
	relocated := relocation.ImageRelocationMap{"web:latest": "ghcr.io/sntns/web@sha256:" + strings.Repeat("a", 64)}

	text, err := registryAuthOf(extended, relocated, credentials)
	require.NoError(t, err)
	var auth map[string]registry.AuthConfig
	require.NoError(t, json.Unmarshal([]byte(text), &auth))
	assert.Equal(t, "bot", auth["ghcr.io"].Username, "where web was relocated")
	assert.Equal(t, "s3cret", auth["ghcr.io"].Password)
	assert.Contains(t, auth, "docker.io", "gill:dev is on Docker Hub")
	assert.NotContains(t, auth, "quay.io", "docker login has nothing for it")

	text, err = registryAuthOf(extended, nil, configfile.New(""))
	require.NoError(t, err)
	assert.Empty(t, text, "no credentials, no parameter")
}
