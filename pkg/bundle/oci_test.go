package bundle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/internal/testimage"
)

func TestPushThenPull(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	directory := t.TempDir()
	composeFile := filepath.Join(directory, "docker-compose.yaml")
	require.NoError(t, os.WriteFile(composeFile, []byte(compose), 0o644))
	domain, installer := filepath.Join(directory, "domain.oci"), filepath.Join(directory, "gill.oci")
	domainDigest := testimage.Write(t, domain)
	testimage.Write(t, installer)

	request := BuildRequest{
		ComposeFiles: []string{composeFile},
		Images:       map[string]string{"sntns-cloud-service-faas-domain:latest": "oci:" + domain},
		Installer:    "oci:" + installer,
	}
	built, err := Build(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, "gill:latest", built.InvocationImages[0].Image)
	assert.NotZero(t, built.InvocationImages[0].Size, "carried with its digest and size")

	sources, err := request.Sources()
	require.NoError(t, err)
	destination := Location{Transport: Layout, Path: filepath.Join(directory, "artifacts", "sntns-cloud-service-faas.oci")}
	_, err = Push(ctx, built, sources, destination)
	require.NoError(t, err)
	_, err = Push(ctx, built, sources, destination)
	require.NoError(t, err, "pushing again changes nothing")

	pulled, err := Pull(ctx, destination)
	require.NoError(t, err)
	assert.Equal(t, built.Name, pulled.Definition.Name)
	assert.Equal(t, "1.4.0", pulled.Definition.Version)
	assert.Equal(t, domainDigest.String(), pulled.Definition.Images["sntns-cloud-service-faas-domain"].Digest)
	assert.Nil(t, pulled.RelocationMap, "a layout's images are on no registry")
	require.NotNil(t, pulled.Definition.Compose, "the compose came along")

	copied := Location{Transport: Layout, Path: destination.Path}
	descriptor, err := copied.Resolve(ctx)
	require.NoError(t, err)
	_, err = os.Stat(newLayout(destination.Path).blob(descriptor.Digest))
	require.NoError(t, err, "the bundle's index is a blob of the layout")
	_, err = os.Stat(newLayout(destination.Path).blob(domainDigest))
	require.NoError(t, err, "the images were copied into the bundle's layout")

	testimage.Write(t, filepath.Join(directory, "rebuilt.oci"))
	rebuilt := Location{Transport: Layout, Path: filepath.Join(directory, "rebuilt.oci")}.Image("sntns-cloud-service-faas-domain:latest")
	_, err = Push(ctx, built, append(sources, rebuilt), destination)
	require.ErrorContains(t, err, "the bundle was built with "+domainDigest.String()+": build it again")
}

func TestCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	directory := t.TempDir()
	composeFile := filepath.Join(directory, "docker-compose.yaml")
	require.NoError(t, os.WriteFile(composeFile, []byte(compose), 0o644))
	domain, installer := filepath.Join(directory, "domain.oci"), filepath.Join(directory, "gill.oci")
	domainDigest := testimage.Write(t, domain)
	testimage.Write(t, installer)
	request := BuildRequest{
		ComposeFiles: []string{composeFile},
		Images:       map[string]string{"sntns-cloud-service-faas-domain:latest": "oci:" + domain},
		Installer:    "oci:" + installer,
	}
	built, err := Build(ctx, request)
	require.NoError(t, err)
	sources, err := request.Sources()
	require.NoError(t, err)
	packaged := Location{Transport: Layout, Path: filepath.Join(directory, "package", "faas.oci")}
	_, err = Push(ctx, built, sources, packaged)
	require.NoError(t, err)

	published := Location{Transport: Layout, Path: filepath.Join(directory, "publish", "faas.oci"), Reference: "latest"}
	_, err = Copy(ctx, packaged, published)
	require.NoError(t, err)

	pulled, err := Pull(ctx, published)
	require.NoError(t, err, "the copy is tagged as the destination names it")
	assert.Equal(t, "1.4.0", pulled.Definition.Version)
	_, err = os.Stat(newLayout(published.Path).blob(domainDigest))
	require.NoError(t, err, "the images came along, from the layout they were in")
}
