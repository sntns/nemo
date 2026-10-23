package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// layout is an OCI image layout directory, seen as a registry of one
// repository: references name the manifests of its index.json by their
// org.opencontainers.image.ref.name, and blobs are files. It lets the same
// code push and pull to a registry and to the directories the sntns
// pipeline builds.
type layout struct {
	path string
	// index serializes the updates of index.json.
	index *sync.Mutex
}

var _ remotes.Resolver = layout{}

func newLayout(path string) layout {
	return layout{path: path, index: &sync.Mutex{}}
}

// tagOf is the part of a reference that names a manifest in the layout:
// its tag, or nothing.
func tagOf(reference string) string {
	if at := strings.LastIndex(reference, "@"); at >= 0 {
		reference = reference[:at]
	}
	if colon := strings.LastIndex(reference, ":"); colon > strings.LastIndex(reference, "/") {
		return reference[colon+1:]
	}
	return ""
}

// Resolve returns the manifest the reference's tag names, or the only
// manifest of the layout when the reference has no tag.
func (layout layout) Resolve(ctx context.Context, reference string) (string, ocispec.Descriptor, error) {
	if at := strings.LastIndex(reference, "@"); at >= 0 {
		dgst, err := digest.Parse(reference[at+1:])
		if err != nil {
			return "", ocispec.Descriptor{}, err
		}
		info, err := os.Stat(layout.blob(dgst))
		if err != nil {
			return "", ocispec.Descriptor{}, fmt.Errorf("%s in %s: %w", dgst, layout.path, errdefs.ErrNotFound)
		}
		mediaType, err := layout.mediaType(dgst)
		if err != nil {
			return "", ocispec.Descriptor{}, err
		}
		return reference, ocispec.Descriptor{MediaType: mediaType, Digest: dgst, Size: info.Size()}, nil
	}

	index, err := layout.readIndex()
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}
	tag := tagOf(reference)
	var found []ocispec.Descriptor
	for _, manifest := range index.Manifests {
		if tag == "" || manifest.Annotations[ocispec.AnnotationRefName] == tag {
			found = append(found, manifest)
		}
	}
	switch {
	case len(found) == 1:
		return reference, found[0], nil
	case len(found) == 0 && tag != "":
		return "", ocispec.Descriptor{}, fmt.Errorf("no manifest named %s in %s: %w", tag, layout.path, errdefs.ErrNotFound)
	case len(found) == 0:
		return "", ocispec.Descriptor{}, fmt.Errorf("no manifest in %s: %w", layout.path, errdefs.ErrNotFound)
	default:
		return "", ocispec.Descriptor{}, fmt.Errorf("%s holds %d manifests: name one with oci:PATH:REFERENCE", layout.path, len(found))
	}
}

func (layout layout) Fetcher(ctx context.Context, reference string) (remotes.Fetcher, error) {
	return remotes.FetcherFunc(func(ctx context.Context, descriptor ocispec.Descriptor) (io.ReadCloser, error) {
		file, err := os.Open(layout.blob(descriptor.Digest))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s in %s: %w", descriptor.Digest, layout.path, errdefs.ErrNotFound)
		}
		return file, err
	}), nil
}

// Pusher writes blobs; a manifest or an index pushed under a tag is also
// named by it in index.json.
func (layout layout) Pusher(ctx context.Context, reference string) (remotes.Pusher, error) {
	tag := tagOf(reference)
	return remotes.PusherFunc(func(ctx context.Context, descriptor ocispec.Descriptor) (content.Writer, error) {
		if _, err := os.Stat(layout.blob(descriptor.Digest)); err == nil {
			if tag != "" && isManifest(descriptor.MediaType) {
				if err := layout.name(descriptor, tag); err != nil {
					return nil, err
				}
			}
			return nil, fmt.Errorf("%s: %w", descriptor.Digest, errdefs.ErrAlreadyExists)
		}
		if err := os.MkdirAll(filepath.Dir(layout.blob(descriptor.Digest)), 0o755); err != nil {
			return nil, err
		}
		file, err := os.CreateTemp(filepath.Dir(layout.blob(descriptor.Digest)), ".ingest-*")
		if err != nil {
			return nil, err
		}
		writer := &blobWriter{layout: layout, file: file, descriptor: descriptor, digester: digest.Canonical.Digester(), started: time.Now()}
		if tag != "" && isManifest(descriptor.MediaType) {
			writer.tag = tag
		}
		return writer, nil
	}), nil
}

func (layout layout) blob(dgst digest.Digest) string {
	return filepath.Join(layout.path, ocispec.ImageBlobsDir, dgst.Algorithm().String(), dgst.Encoded())
}

func (layout layout) readIndex() (ocispec.Index, error) {
	var index ocispec.Index
	data, err := os.ReadFile(filepath.Join(layout.path, ocispec.ImageIndexFile))
	if errors.Is(err, fs.ErrNotExist) {
		return ocispec.Index{Versioned: index.Versioned, MediaType: ocispec.MediaTypeImageIndex}, nil
	}
	if err != nil {
		return index, err
	}
	return index, json.Unmarshal(data, &index)
}

// name names the manifest by the tag in index.json, in place of the
// manifest the tag named before.
func (layout layout) name(descriptor ocispec.Descriptor, tag string) error {
	layout.index.Lock()
	defer layout.index.Unlock()
	index, err := layout.readIndex()
	if err != nil {
		return err
	}
	index.SchemaVersion = 2
	index.Manifests = slices.DeleteFunc(index.Manifests, func(manifest ocispec.Descriptor) bool {
		return manifest.Annotations[ocispec.AnnotationRefName] == tag
	})
	descriptor.Annotations = map[string]string{ocispec.AnnotationRefName: tag}
	index.Manifests = append(index.Manifests, ocispec.Descriptor{MediaType: descriptor.MediaType, Digest: descriptor.Digest, Size: descriptor.Size, Annotations: descriptor.Annotations})
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(layout.path, ocispec.ImageLayoutFile), []byte(`{"imageLayoutVersion":"`+ocispec.ImageLayoutVersion+`"}`), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(layout.path, ocispec.ImageIndexFile), data, 0o644)
}

// mediaType reads the media type a manifest or an index declares.
func (layout layout) mediaType(dgst digest.Digest) (string, error) {
	data, err := os.ReadFile(layout.blob(dgst))
	if err != nil {
		return "", err
	}
	var declared struct {
		MediaType string `json:"mediaType"`
	}
	if err := json.Unmarshal(data, &declared); err != nil {
		return "", fmt.Errorf("%s in %s is not a manifest: %w", dgst, layout.path, err)
	}
	return declared.MediaType, nil
}

// blobWriter writes a blob to a temporary file, and moves it in place
// once its digest is checked.
type blobWriter struct {
	layout     layout
	file       *os.File
	descriptor ocispec.Descriptor
	digester   digest.Digester
	offset     int64
	started    time.Time
	tag        string
}

func (writer *blobWriter) Write(data []byte) (int, error) {
	written, err := writer.file.Write(data)
	writer.digester.Hash().Write(data[:written])
	writer.offset += int64(written)
	return written, err
}

func (writer *blobWriter) Close() error {
	if writer.file == nil {
		return nil
	}
	err := writer.file.Close()
	os.Remove(writer.file.Name())
	writer.file = nil
	return err
}

func (writer *blobWriter) Digest() digest.Digest { return writer.digester.Digest() }

func (writer *blobWriter) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...content.Opt) error {
	defer writer.Close()
	if expected == "" {
		expected = writer.descriptor.Digest
	}
	if writer.Digest() != expected {
		return fmt.Errorf("blob %s: written content has digest %s", expected, writer.Digest())
	}
	if size > 0 && size != writer.offset {
		return fmt.Errorf("blob %s: written %d bytes, expected %d", expected, writer.offset, size)
	}
	if err := writer.file.Close(); err != nil {
		return err
	}
	if err := os.Rename(writer.file.Name(), writer.layout.blob(expected)); err != nil {
		return err
	}
	writer.file = nil
	if writer.tag != "" {
		return writer.layout.name(writer.descriptor, writer.tag)
	}
	return nil
}

func (writer *blobWriter) Status() (content.Status, error) {
	return content.Status{Ref: writer.descriptor.Digest.String(), Offset: writer.offset, Total: writer.descriptor.Size, Expected: writer.descriptor.Digest, StartedAt: writer.started, UpdatedAt: time.Now()}, nil
}

func (writer *blobWriter) Truncate(size int64) error {
	if size != 0 {
		return errors.New("blobWriter: only truncating to 0 is supported")
	}
	if err := writer.file.Truncate(0); err != nil {
		return err
	}
	if _, err := writer.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	writer.offset, writer.digester = 0, digest.Canonical.Digester()
	return nil
}

func isManifest(mediaType string) bool {
	switch mediaType {
	case ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageIndex, images.MediaTypeDockerSchema2Manifest, images.MediaTypeDockerSchema2ManifestList:
		return true
	}
	return false
}
