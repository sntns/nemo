package volume

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
