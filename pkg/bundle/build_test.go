package bundle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/internal/testimage"
	"github.com/sntns/nemo/pkg/cnab"
)

const compose = `version: '3.9'
x-nemo-bundle:
  name: sntns-cloud-service-faas
  version: 1.4.0
  dependencies:
    requires:
      messaging:
        bundle: ghcr.io/sntns/sntns-cloud-infrastructure-messaging
        version:
          ranges: ['*']
services:
  domain:
    image: sntns-cloud-service-faas-domain:latest
    environment:
      LOCATION_REGION: ${REGION}
      PORT: ${PORT:-8080}
    configs: [page]
    secrets: [token]
configs:
  page:
    content: hello
secrets:
  token:
    environment: API_TOKEN
`

func TestBuild(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	composeFile := filepath.Join(directory, "docker-compose.yaml")
	require.NoError(t, os.WriteFile(composeFile, []byte(compose), 0o644))
	layout := filepath.Join(directory, "domain.oci")
	manifest := testimage.Write(t, layout)

	built, err := Build(context.Background(), BuildRequest{
		ComposeFiles: []string{composeFile},
		Version:      "1.4.1",
		Images:       map[string]string{"sntns-cloud-service-faas-domain:latest": "oci:" + layout},
		Installer:    "gill:dev",
	})
	require.NoError(t, err)

	assert.Equal(t, "sntns-cloud-service-faas", built.Name)
	assert.Equal(t, "1.4.1", built.Version, "the flag over x-nemo-bundle")
	image := built.Images["sntns-cloud-service-faas-domain"]
	assert.Equal(t, "sntns-cloud-service-faas-domain:latest", image.Image, "the name the compose gives")
	assert.Equal(t, manifest.String(), image.Digest)

	assert.Equal(t, "REGION", built.Parameters["region"].Destination.EnvironmentVariable)
	assert.Equal(t, "8080", built.Definitions["port"].Default)
	assert.Contains(t, built.Parameters, cnab.ParameterOrchestrator)
	assert.Equal(t, "API_TOKEN", built.Credentials["api-token"].EnvironmentVariable, "a secret read from a variable is a credential")

	require.NotNil(t, built.Dependencies)
	dependency := built.Dependencies.Requires["messaging"]
	assert.Equal(t, "ghcr.io/sntns/sntns-cloud-infrastructure-messaging", dependency.Bundle, "as io.cnab.dependencies declares it")
	assert.Equal(t, []string{"*"}, dependency.Version.Ranges)

	require.NotNil(t, built.Compose)
	assert.NotContains(t, built.Compose.Documents[0].Config, Extension)
	assert.Equal(t, "${REGION}", built.Compose.Documents[0].Config["services"].(map[string]any)["domain"].(map[string]any)["environment"].(map[string]any)["LOCATION_REGION"], "left for the installer to interpolate")
}

func TestBuild_refusals(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	composeFile := filepath.Join(directory, "docker-compose.yaml")
	require.NoError(t, os.WriteFile(composeFile, []byte(compose+"  file:\n    file: ./key.pem\n"), 0o644))

	_, err := Build(context.Background(), BuildRequest{
		ComposeFiles: []string{composeFile},
		Images:       map[string]string{"other:latest": "docker://ghcr.io/sntns/other:1.0"},
		Installer:    "gill:dev",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services.domain.image: sntns-cloud-service-faas-domain:latest has no location")
	assert.Contains(t, err.Error(), "--image other:latest: no service of the compose has this image")
	assert.Contains(t, err.Error(), "secrets.file: a bundle carries no files")
}

func TestParseLocation(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]Location{
		"docker://ghcr.io/sntns/faas:1.0": {Transport: Registry, Reference: "ghcr.io/sntns/faas:1.0"},
		"oci:/artifacts/faas.oci":         {Transport: Layout, Path: "/artifacts/faas.oci"},
		"oci:/artifacts/faas.oci:1.0":     {Transport: Layout, Path: "/artifacts/faas.oci", Reference: "1.0"},
	} {
		location, err := ParseLocation(text)
		require.NoError(t, err, text)
		assert.Equal(t, want, location, text)
		assert.Equal(t, text, location.String())
	}
	_, err := ParseLocation("ghcr.io/sntns/faas")
	require.Error(t, err)
}
