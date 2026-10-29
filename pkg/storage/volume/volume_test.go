package volume

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/target"
)

func archiveOf(t *testing.T, headers ...tar.Header) *bytes.Buffer {
	t.Helper()
	var content bytes.Buffer
	archive := tar.NewWriter(&content)
	for _, header := range headers {
		data := []byte(header.Linkname)
		if header.Typeflag == tar.TypeReg {
			header.Size, header.Linkname = int64(len(data)), ""
		}
		require.NoError(t, archive.WriteHeader(&header))
		if header.Typeflag == tar.TypeReg {
			_, err := archive.Write(data)
			require.NoError(t, err)
		}
	}
	require.NoError(t, archive.Close())
	return &content
}

func TestExtract(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	// Linkname carries a file's content, for archiveOf.
	require.NoError(t, extract(archiveOf(t,
		tar.Header{Typeflag: tar.TypeDir, Name: "records/"},
		tar.Header{Typeflag: tar.TypeDir, Name: "records/installations/web/"},
		tar.Header{Typeflag: tar.TypeReg, Name: "records/installations/web/installation.json", Linkname: "{}"},
		tar.Header{Typeflag: tar.TypeReg, Name: "records/owner", Linkname: "terraform\n"},
	), directory))
	data, err := os.ReadFile(filepath.Join(directory, "installations", "web", "installation.json"))
	require.NoError(t, err)
	assert.Equal(t, "{}", string(data), "under the volume's directory")

	outside := extract(archiveOf(t, tar.Header{Typeflag: tar.TypeReg, Name: "records/../../escape", Linkname: "x"}), t.TempDir())
	assert.ErrorContains(t, outside, "not a path in the volume")
	link := extract(archiveOf(t, tar.Header{Typeflag: tar.TypeSymlink, Name: "records/link", Linkname: "/etc/passwd"}), t.TempDir())
	assert.ErrorContains(t, link, "neither a file nor a directory")
}

func TestRecordsVolume(t *testing.T) {
	t.Parallel()
	spec, provision, err := recordsVolume("shop", nil)
	require.NoError(t, err)
	assert.Equal(t, "nemo-records-shop", spec.Name)
	assert.Empty(t, spec.Driver, "local, on the Docker nemo reaches")
	assert.Nil(t, provision)

	nfs := map[string]target.VolumeType{Type: {
		VolumeDriver: target.VolumeDriver{Driver: "local", Options: map[string]string{"type": "nfs", "o": "addr=10.0.0.20,rw", "device": ":/exports/{{.Stack}}/{{.Name}}"}},
		Provision:    &target.VolumeProvision{Directory: "{{.Stack}}/{{.Name}}", Volume: target.VolumeDriver{Options: map[string]string{"type": "nfs", "device": ":/exports"}}},
	}}
	spec, provision, err = recordsVolume("shop", nfs)
	require.NoError(t, err)
	assert.Equal(t, ":/exports/shop/nemo-records", spec.Options["device"], "the storage every manager mounts")
	assert.Equal(t, "shop/nemo-records", provision.Directory)
	assert.Equal(t, map[string]string{Label: "shop"}, spec.Labels)

	_, _, err = recordsVolume("shop", map[string]target.VolumeType{Type: {VolumeDriver: target.VolumeDriver{Driver: "{{.Bundle}}"}}})
	assert.ErrorContains(t, err, "volume type records: driver")
}
