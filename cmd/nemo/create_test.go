package main

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/internal/testimage"
	"github.com/sntns/nemo/pkg/bundle"
	"github.com/sntns/nemo/pkg/cnab"
)

func TestLayoutBundles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	directory := t.TempDir()
	compose := filepath.Join(directory, "docker-compose.yaml")
	require.NoError(t, os.WriteFile(compose, []byte(`
x-nemo-bundle: { name: web, version: 1.0.0 }
services:
  web:
    image: web:latest
    volumes: [ "data:/data" ]
volumes:
  data:
    x-nemo-volume: { type: data }
`), 0o644))
	image, installer := filepath.Join(directory, "web-image.oci"), filepath.Join(directory, "gill.oci")
	testimage.Write(t, image)
	testimage.Write(t, installer)
	request := bundle.BuildRequest{ComposeFiles: []string{compose}, Images: map[string]string{"web:latest": "oci:" + image}, Installer: "oci:" + installer}
	built, err := bundle.Build(ctx, request)
	require.NoError(t, err)
	sources, err := request.Sources()
	require.NoError(t, err)
	layouts := filepath.Join(directory, "layouts")
	_, err = bundle.Push(ctx, built, sources, bundle.Location{Transport: bundle.Layout, Path: filepath.Join(layouts, "web.oci")})
	require.NoError(t, err)
	testimage.Write(t, filepath.Join(layouts, "gill.oci"))

	definitions, err := layoutBundles(ctx, layouts)
	require.NoError(t, err)
	assert.Equal(t, []string{"web"}, slices.Sorted(maps.Keys(definitions)), "the installer's image layout is no bundle")
	assert.Equal(t, []string{"data"}, volumeTypes([]cnab.ExtendedBundle{definitions["web"]}), "the volume types its compose uses")

	_, err = layoutBundles(ctx, directory)
	require.ErrorContains(t, err, "holds no NAME.oci layout of a bundle NAME")
}
