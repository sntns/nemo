//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	mobyvolume "github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/storage/volume"
	"github.com/sntns/nemo/pkg/target"
)

// A stack's records on its target: the first nemo to manage it, Terraform's
// provider on its first apply, records itself before any installation. On a
// swarm manager, a Swarm config holds the stack while it writes.
func TestRecords_ownerOfANewStack(t *testing.T) {
	b := newBench(t, "compose")
	b.after = append(b.after, func() {
		_, _ = b.api.VolumeRemove(context.WithoutCancel(t.Context()), volume.Name(b.stack), client.VolumeRemoveOptions{Force: true})
	})

	held, err := volume.Open(t.Context(), b.api, b.stack, nil, true)
	require.NoError(t, err)
	assert.Empty(t, held.Owner())
	_, err = volume.Open(t.Context(), b.api, b.stack, nil, true)
	assert.ErrorIs(t, err, volume.ErrBusy, "one writer at a time")
	info, err := b.api.Info(t.Context(), client.InfoOptions{})
	require.NoError(t, err)
	if info.Info.Swarm.ControlAvailable {
		_, err := b.api.ConfigInspect(t.Context(), volume.Name(b.stack), client.ConfigInspectOptions{})
		assert.NoError(t, err, "the swarm's config, unique in the whole swarm")
	}
	require.NoError(t, held.SetOwner(t.Context(), "terraform"))
	require.NoError(t, held.Close(t.Context()))
	if info.Info.Swarm.ControlAvailable {
		_, err := b.api.ConfigInspect(t.Context(), volume.Name(b.stack), client.ConfigInspectOptions{})
		assert.Error(t, err, "the stack freed")
	}

	read, err := volume.Open(t.Context(), b.api, b.stack, nil, false)
	require.NoError(t, err)
	defer read.Close(t.Context())
	assert.Equal(t, "terraform", read.Owner())
	installations, err := read.ListInstallations(t.Context(), b.stack)
	require.NoError(t, err)
	assert.Empty(t, installations)
}

// sharedRecords is a records type on a directory of the Docker's host, which
// nemo provisions: as an NFS export is for the managers of a swarm.
func sharedRecords(t *testing.T, b *bench) map[string]target.VolumeType {
	t.Helper()
	b.after = append(b.after, func() {
		_, _ = b.api.VolumeRemove(context.WithoutCancel(t.Context()), volume.Name(b.stack), client.VolumeRemoveOptions{Force: true})
		removeFromHost(t, b.api, "nemo-it/"+b.stack)
	})
	return map[string]target.VolumeType{volume.Type: {
		VolumeDriver: target.VolumeDriver{Driver: "local", Options: map[string]string{"type": "none", "o": "bind", "device": "/tmp/nemo-it/{{.Stack}}/{{.Name}}"}},
		Provision: &target.VolumeProvision{
			Directory: "nemo-it/{{.Stack}}/{{.Name}}",
			Volume:    target.VolumeDriver{Driver: "local", Options: map[string]string{"type": "none", "o": "bind", "device": "/tmp"}},
		},
	}}
}

// removeFromHost removes a directory of the /tmp of the Docker's host.
func removeFromHost(t *testing.T, api client.APIClient, directory string) {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	daemon := docker.New(api)
	host := &mobyvolume.Volume{Name: "nemo-it-host-" + strings.ReplaceAll(directory, "/", "-"), Driver: "local", Options: map[string]string{"type": "none", "o": "bind", "device": "/tmp"}}
	require.NoError(t, daemon.VolumeCreate(ctx, host))
	defer func() { _ = daemon.VolumeDelete(ctx, host) }()
	require.NoError(t, daemon.ImageEnsure(ctx, "busybox:1.37", "", ""))
	id, err := daemon.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     &container.Config{Image: "busybox:1.37", Cmd: []string{"rm", "-rf", "/host/" + directory}},
		HostConfig: &container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: host.Name, Target: "/host"}}},
	})
	require.NoError(t, err)
	defer func() { _ = daemon.ContainerDelete(ctx, id) }()
	require.NoError(t, daemon.ContainerStart(ctx, id))
	code, err := daemon.ContainerWait(ctx, id)
	require.NoError(t, err)
	assert.Zero(t, code, "removing %s", directory)
}

func web(stack string) storage.Installation {
	return storage.Installation{InstallationSpec: storage.InstallationSpec{Name: "web", Stack: stack}}
}

// The records of a target with a records type are on its storage: a
// Docker of the swarm without the volume yet makes one of the type, and
// finds them there.
func TestRecords_onTheStorageOfTheRecordsType(t *testing.T) {
	b := newBench(t, "compose")
	volumes := sharedRecords(t, b)

	held, err := volume.Open(t.Context(), b.api, b.stack, volumes, true)
	require.NoError(t, err)
	require.NoError(t, held.SaveInstallation(t.Context(), web(b.stack)))
	require.NoError(t, held.Close(t.Context()))
	inspected, err := b.api.VolumeInspect(t.Context(), volume.Name(b.stack), client.VolumeInspectOptions{})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/nemo-it/"+b.stack+"/nemo-records", inspected.Volume.Options["device"], "provisioned, then made of the type")

	_, err = b.api.VolumeRemove(t.Context(), volume.Name(b.stack), client.VolumeRemoveOptions{})
	require.NoError(t, err, "as on another manager, which never had the volume")
	read, err := volume.Open(t.Context(), b.api, b.stack, volumes, false)
	require.NoError(t, err)
	defer read.Close(t.Context())
	installations, err := read.ListInstallations(t.Context(), b.stack)
	require.NoError(t, err)
	require.Len(t, installations, 1, "the records are on the storage, not in the volume")
	assert.Equal(t, "web", installations[0].Name)
}

// A stack whose target gets a records type moves its records there at its
// next write; when the storage has records of the stack already, moved
// there by another Docker, they are the stack's, and the volume's are kept
// aside.
func TestRecords_moveToTheRecordsType(t *testing.T) {
	b := newBench(t, "compose")
	volumes := sharedRecords(t, b)

	local, err := volume.Open(t.Context(), b.api, b.stack, nil, true)
	require.NoError(t, err)
	require.NoError(t, local.SaveInstallation(t.Context(), web(b.stack)))
	require.NoError(t, local.SetOwner(t.Context(), "terraform"))
	require.NoError(t, local.Close(t.Context()))

	read, err := volume.Open(t.Context(), b.api, b.stack, volumes, false)
	require.NoError(t, err)
	require.NoError(t, read.Close(t.Context()), "reading moves nothing")

	moved, err := volume.Open(t.Context(), b.api, b.stack, volumes, true)
	require.NoError(t, err)
	installations, err := moved.ListInstallations(t.Context(), b.stack)
	require.NoError(t, err)
	assert.Len(t, installations, 1)
	assert.Equal(t, "terraform", moved.Owner())
	require.NoError(t, moved.Close(t.Context()))
	inspected, err := b.api.VolumeInspect(t.Context(), volume.Name(b.stack), client.VolumeInspectOptions{})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/nemo-it/"+b.stack+"/nemo-records", inspected.Volume.Options["device"])

	_, err = b.api.VolumeRemove(t.Context(), volume.Name(b.stack), client.VolumeRemoveOptions{})
	require.NoError(t, err)
	other, err := volume.Open(t.Context(), b.api, b.stack, nil, true)
	require.NoError(t, err, "a Docker of the swarm, before it had the type, writes a local volume")
	api := web(b.stack)
	api.Name = "api"
	require.NoError(t, other.SaveInstallation(t.Context(), api))
	require.NoError(t, other.Close(t.Context()))
	_, err = volume.Open(t.Context(), b.api, b.stack, volumes, true)
	require.ErrorContains(t, err, "the storage of the type has records of the stack already")
	_, kept, _ := strings.Cut(err.Error(), "they are kept in ")
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(kept)) })
	assert.FileExists(t, kept+"/installations/api/installation.json", "the volume's, kept aside")

	read, err = volume.Open(t.Context(), b.api, b.stack, volumes, false)
	require.NoError(t, err)
	defer read.Close(t.Context())
	installations, err = read.ListInstallations(t.Context(), b.stack)
	require.NoError(t, err)
	require.Len(t, installations, 1)
	assert.Equal(t, "web", installations[0].Name, "the storage's")
}
