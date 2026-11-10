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

// The build knows no value of the variables: a typed field that needs one is valid whatever it
// will be, and one whose variable has a default is checked with it.
func TestBuild_variablesInTypedFields(t *testing.T) {
	t.Parallel()
	build := func(t *testing.T, service string) error {
		directory := t.TempDir()
		composeFile := filepath.Join(directory, "compose.yaml")
		layout := filepath.Join(directory, "web.oci")
		testimage.Write(t, layout)
		project := "x-nemo-bundle:\n  name: web\n  version: 1.0.0\nservices:\n  web:\n    image: web:latest\n" + service
		require.NoError(t, os.WriteFile(composeFile, []byte(project), 0o644))
		_, err := Build(context.Background(), BuildRequest{
			ComposeFiles: []string{composeFile},
			Images:       map[string]string{"web:latest": "oci:" + layout},
			Installer:    "gill:dev",
		})
		return err
	}

	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, build(t, "    ports: [\"${WEB_PORT}:80\"]\n    deploy:\n      replicas: ${REPLICAS}\n"))
	})
	t.Run("a valid default", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, build(t, "    ports: [\"${WEB_PORT:-8080}:80\"]\n"))
	})
	t.Run("a config's content", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, build(t, "    configs: [page]\nconfigs:\n  page:\n    content: 'served by ${HOST}'\n"))
	})
	t.Run("an invalid default", func(t *testing.T) {
		t.Parallel()
		require.Error(t, build(t, "    deploy:\n      replicas: ${REPLICAS:-two}\n"))
	})
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

// A database gives its URI to the bundles that require it: its own, or the one it is given.
const database = `x-nemo-bundle:
  name: database
  version: 1.0.0
  outputs:
    uri:
      description: The URI of the database.
      value: ${DATABASE_URI:-postgres://app:secret@database:5432/app}
      sensitive: true
services:
  database:
    image: database:latest
`

// The application reads the database's URI from its output.
const application = `x-nemo-bundle:
  name: application
  version: 1.0.0
  dependencies:
    requires:
      database:
        bundle: ghcr.io/example/database
  parameters:
    database-uri:
      source:
        dependency: database
        output: uri
services:
  application:
    image: application:latest
    environment:
      DATABASE_URI: ${DATABASE_URI}
`

func buildCompose(t *testing.T, project, image string) (cnab.ExtendedBundle, error) {
	t.Helper()
	directory := t.TempDir()
	composeFile := filepath.Join(directory, "compose.yaml")
	layout := filepath.Join(directory, "image.oci")
	testimage.Write(t, layout)
	require.NoError(t, os.WriteFile(composeFile, []byte(project), 0o644))
	return Build(context.Background(), BuildRequest{
		ComposeFiles: []string{composeFile},
		Images:       map[string]string{image: "oci:" + layout},
		Installer:    "gill:dev",
	})
}

func TestBuild_outputs(t *testing.T) {
	t.Parallel()
	built, err := buildCompose(t, database, "database:latest")
	require.NoError(t, err)

	output := built.Outputs["uri"]
	assert.Equal(t, "/cnab/app/outputs/uri", output.Path)
	assert.Equal(t, []string{cnab.ActionInstall, cnab.ActionUpgrade}, output.ApplyTo)
	assert.Equal(t, "The URI of the database.", output.Description)
	definition := built.Definitions[output.Definition]
	require.NotNil(t, definition.WriteOnly)
	assert.True(t, *definition.WriteOnly, "sensitive")

	require.NotNil(t, built.OutputValues)
	assert.Equal(t, "${DATABASE_URI:-postgres://app:secret@database:5432/app}", (*built.OutputValues)["uri"].Value, "left for the installer to interpolate")
	assert.Equal(t, "DATABASE_URI", built.Parameters["database-uri"].Destination.EnvironmentVariable, "a variable of an output is a parameter")

	value, err := built.OutputValue("uri", built.OutputLookup(nil))
	require.NoError(t, err)
	assert.Equal(t, "postgres://app:secret@database:5432/app", value, "its own")
	value, err = built.OutputValue("uri", built.OutputLookup(map[string]string{"database-uri": "postgres://managed:5432/app"}))
	require.NoError(t, err)
	assert.Equal(t, "postgres://managed:5432/app", value, "the one it is given")
}

func TestBuild_parameterSources(t *testing.T) {
	t.Parallel()
	built, err := buildCompose(t, application, "application:latest")
	require.NoError(t, err)

	require.NotNil(t, built.ParameterSources)
	dependency, output, ok := (*built.ParameterSources)["database-uri"].DependencyOutput()
	assert.True(t, ok)
	assert.Equal(t, "database", dependency)
	assert.Equal(t, "uri", output)
	assert.True(t, built.IsSourced("database-uri"))
	assert.Contains(t, built.RequiredExtensions, cnab.ParameterSourcesExtension.Key)
	assert.NoError(t, built.CheckRequiredExtensions(), "nemo honours its source")
}

func TestBuild_outputsAndSourcesRefused(t *testing.T) {
	t.Parallel()
	_, err := buildCompose(t, `x-nemo-bundle:
  name: application
  version: 1.0.0
  dependencies:
    requires:
      database:
        bundle: ghcr.io/example/database
  outputs:
    URL:
      value: http://application
    empty:
      value: ""
  parameters:
    cache-uri:
      source:
        dependency: database
        output: uri
    database-uri:
      source:
        dependency: cache
        output: uri
services:
  application:
    image: application:latest
    environment:
      DATABASE_URI: ${DATABASE_URI}
`, "application:latest")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outputs.URL: an output is named with lowercase letters, digits and dashes")
	assert.Contains(t, err.Error(), "outputs.empty: give it a value")
	assert.Contains(t, err.Error(), "parameters.cache-uri: no variable of the compose is this parameter")
	assert.Contains(t, err.Error(), "parameters.database-uri.source.dependency: cache is not in dependencies.requires")
}
