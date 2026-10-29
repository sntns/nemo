package docker

import (
	"archive/tar"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvisionArchive(t *testing.T) {
	t.Parallel()
	content, err := provisionArchive("shop/data/")
	require.NoError(t, err)
	var names []string
	entries := tar.NewReader(content)
	for header, err := entries.Next(); err == nil; header, err = entries.Next() {
		names = append(names, header.Name)
	}
	assert.Equal(t, []string{"shop/", "shop/data/"}, names, "the directory and its parents")

	for _, directory := range []string{"/etc", "../etc", "shop/../../etc", ".", ""} {
		_, err := provisionArchive(directory)
		assert.Error(t, err, directory)
	}
}
