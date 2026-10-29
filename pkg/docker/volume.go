package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"

	"github.com/sntns/nemo/pkg/target"
)

// provisionMount is where a provisioning container mounts the storage.
const provisionMount = "/provision"

func (docker *Client) VolumeCreate(ctx context.Context, spec *volume.Volume) error {
	options := client.VolumeCreateOptions{Name: spec.Name, Driver: spec.Driver, DriverOpts: spec.Options, Labels: spec.Labels}
	if _, err := docker.api.VolumeCreate(ctx, options); err != nil {
		return fmt.Errorf("creating volume %s: %w", spec.Name, err)
	}
	return nil
}

// VolumeFind returns the volume of that name, or nil.
func (docker *Client) VolumeFind(ctx context.Context, name string) (*volume.Volume, error) {
	result, err := docker.api.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	switch {
	case IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("inspecting volume %s: %w", name, err)
	}
	return &result.Volume, nil
}

// VolumeDelete removes the volume, and its data.
func (docker *Client) VolumeDelete(ctx context.Context, volume *volume.Volume) error {
	if _, err := docker.api.VolumeRemove(ctx, volume.Name, client.VolumeRemoveOptions{}); err != nil && !IsNotFound(err) {
		return fmt.Errorf("removing volume %s: %w", volume.Name, err)
	}
	return nil
}

// SameVolume reports whether an existing volume has the driver and options
// the spec declares. No driver declared means the daemon's default, local.
func SameVolume(existing, spec *volume.Volume) bool {
	driver := spec.Driver
	if driver == "" {
		driver = "local"
	}
	return existing.Driver == driver && maps.Equal(existing.Options, spec.Options)
}

// VolumeProvision creates the directory of a volume in the storage that
// provision.Volume mounts: a temporary volume of that storage is mounted in
// a container of the image that never starts, and Docker's archive API
// writes the directory there.
func (docker *Client) VolumeProvision(ctx context.Context, image, registryAuth string, provision target.VolumeProvision) error {
	directory, err := provisionArchive(provision.Directory)
	if err != nil {
		return err
	}
	storage := &volume.Volume{Name: "nemo-provision-" + rand.Text(), Driver: provision.Volume.Driver, Options: provision.Volume.Options}
	if err := docker.VolumeCreate(ctx, storage); err != nil {
		return err
	}
	defer func() { _ = docker.VolumeDelete(context.WithoutCancel(ctx), storage) }()

	if err := docker.ImageEnsure(ctx, image, "", registryAuth); err != nil {
		return err
	}
	id, err := docker.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{Image: image, Cmd: []string{"provision"}},
		HostConfig: &container.HostConfig{
			Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: storage.Name, Target: provisionMount}},
		},
		Name: storage.Name,
	})
	if err != nil {
		return err
	}
	defer func() { _ = docker.ContainerDelete(context.WithoutCancel(ctx), id) }()

	if _, err := docker.api.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: provisionMount, Content: directory}); err != nil {
		return fmt.Errorf("provisioning %s: %w", provision.Directory, err)
	}
	return nil
}

// provisionArchive is the archive of the directory and its parents, which
// must be in the storage.
func provisionArchive(directory string) (*bytes.Buffer, error) {
	clean := path.Clean(directory)
	if !fs.ValidPath(clean) || clean == "." {
		return nil, fmt.Errorf("provisioning %s: not a directory in the storage", directory)
	}
	var content bytes.Buffer
	archive := tar.NewWriter(&content)
	parts := strings.Split(clean, "/")
	for i := range parts {
		if err := archive.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: strings.Join(parts[:i+1], "/") + "/", Mode: 0o755}); err != nil {
			return nil, err
		}
	}
	return &content, archive.Close()
}
