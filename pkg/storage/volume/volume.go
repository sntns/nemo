// Package volume keeps the records of a stack's installations in a volume
// of its target, where whoever reaches the target finds them: the CI that
// deployed the stack, and anyone who imported its manifest.
//
// The volume is reached through a container that never starts, of an empty
// image nemo makes on the target: Docker's archive API copies the records
// in and out of it, over the connection to the target, with nothing
// running. Opened, the store copies the volume into a local directory,
// which the file store serves; each write goes back to the volume.
//
// The volume is local to the Docker nemo reaches, unless the target has a
// volume type records: on a swarm of several managers, a storage they all
// mount keeps the same records whichever one nemo reaches.
//
// One nemo writes a stack at a time: it holds the stack with a Swarm config
// named after it on a swarm manager, whose name the whole swarm keeps
// unique, else with a container named after it, unique on its Docker.
package volume

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/jsonmessage"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/storage/file"
	"github.com/sntns/nemo/pkg/target"
)

const (
	// Label names the stack whose records a volume or a container holds.
	// It is not the stack's own label, which uninstalling a stack selects.
	Label = "io.nemo.records"
	// Image is the empty image of the containers that reach the volumes.
	Image = "nemo-records:1"
	// Type is the volume type of the target that makes the records'
	// volume, whose {{.Name}} is nemo-records.
	Type = "records"
	// mountPoint is where the volume is in the container.
	mountPoint = "/records"
	// ownerFile names who manages the stack, when not nemo itself.
	ownerFile = "owner"
)

// ErrBusy is returned when another nemo holds the stack for writing.
var ErrBusy = errors.New("busy")

// Name is the name of the volume, and of the config or the container that
// holds the stack.
func Name(stack string) string {
	return "nemo-records-" + stack
}

var dockerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// Store is the store of one stack, from Open to Close.
type Store struct {
	docker *docker.Client
	stack  string
	// container reaches the volume.
	container string
	// release frees the stack, held for writing.
	release func(context.Context) error
	mirror  string
	local   *file.Store
	owner   string
}

var _ storage.Store = (*Store)(nil)

// Open copies the records of the stack from its volume, which it creates the
// first time, of the volumes' records type when they have one. Held, it is
// the stack's only writer until Close: another Open held fails with ErrBusy;
// and it moves to a volume of that type the records of a volume that is
// not of it.
func Open(ctx context.Context, api client.APIClient, stack string, volumes map[string]target.VolumeType, held bool) (_ *Store, err error) {
	if !dockerName.MatchString(stack) {
		return nil, fmt.Errorf("stack %s: its records are kept on the target in a volume named after it, which Docker refuses: letters, digits, _ . and - only", stack)
	}
	spec, provision, err := recordsVolume(stack, volumes)
	if err != nil {
		return nil, err
	}
	store := &Store{docker: docker.New(api), stack: stack}
	defer func() {
		if err != nil {
			err = errors.Join(err, store.Close(ctx))
		}
	}()
	if err := store.ensureImage(ctx); err != nil {
		return nil, err
	}
	if held {
		if err := store.hold(ctx); err != nil {
			return nil, err
		}
	}
	existing, err := store.docker.VolumeFind(ctx, spec.Name)
	switch {
	case err != nil:
		return nil, err
	case existing == nil:
		if err := store.createVolume(ctx, spec, provision); err != nil {
			return nil, err
		}
	case held && !docker.SameVolume(existing, spec):
		if err := store.move(ctx, spec, provision); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err := store.attach(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

// recordsVolume is the volume of the stack's records: of the records type
// when the volumes have one, else local.
func recordsVolume(stack string, volumes map[string]target.VolumeType) (*volume.Volume, *target.VolumeProvision, error) {
	spec := &volume.Volume{Name: Name(stack), Labels: map[string]string{Label: stack}}
	volumeType, ok := volumes[Type]
	if !ok {
		return spec, nil, nil
	}
	rendered, err := volumeType.Render(stack, "nemo-records", nil)
	if err != nil {
		return nil, nil, fmt.Errorf("volume type %s: %w", Type, err)
	}
	spec.Driver, spec.Options = rendered.Driver, rendered.Options
	return spec, rendered.Provision, nil
}

func (store *Store) ensureImage(ctx context.Context) error {
	if _, err := store.docker.ImageInspect(ctx, Image); err == nil {
		return nil
	}
	// An image of a single directory, where the volume is mounted.
	var layer bytes.Buffer
	archive := tar.NewWriter(&layer)
	if err := archive.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: strings.TrimPrefix(mountPoint, "/") + "/", Mode: 0o700}); err != nil {
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}
	result, err := store.docker.API().ImageImport(ctx, client.ImageImportSource{Source: &layer, SourceName: "-"}, Image, client.ImageImportOptions{})
	if err != nil {
		return fmt.Errorf("making the image %s on the target: %w", Image, err)
	}
	defer result.Close()
	if err := jsonmessage.DisplayJSONMessagesStream(result, io.Discard, 0, false, nil); err != nil {
		return fmt.Errorf("making the image %s on the target: %w", Image, err)
	}
	return nil
}

func (store *Store) createVolume(ctx context.Context, spec *volume.Volume, provision *target.VolumeProvision) error {
	if provision != nil {
		if err := store.docker.VolumeProvision(ctx, Image, "", *provision); err != nil {
			return err
		}
	}
	return store.docker.VolumeCreate(ctx, spec)
}

// hold makes the store the stack's only writer, with a Swarm config named
// after the stack on a swarm manager, else with a container.
func (store *Store) hold(ctx context.Context) error {
	info, err := store.docker.API().Info(ctx, client.InfoOptions{})
	if err != nil {
		return err
	}
	name := Name(store.stack)
	if info.Info.Swarm.ControlAvailable {
		host, _ := os.Hostname()
		created, err := store.docker.API().ConfigCreate(ctx, client.ConfigCreateOptions{Spec: swarm.ConfigSpec{
			Annotations: swarm.Annotations{Name: name, Labels: map[string]string{Label: store.stack}},
			Data:        []byte(host + "\n"),
		}})
		if errdefs.IsConflict(err) || errdefs.IsAlreadyExists(err) {
			return store.busy(ctx, name, true)
		}
		if err != nil {
			return err
		}
		store.release = func(ctx context.Context) error {
			_, err := store.docker.API().ConfigRemove(ctx, created.ID, client.ConfigRemoveOptions{})
			return err
		}
		return nil
	}
	id, err := store.docker.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:   name,
		Config: &container.Config{Image: Image, Cmd: []string{"records"}, Labels: map[string]string{Label: store.stack}},
	})
	if errdefs.IsConflict(err) {
		return store.busy(ctx, name, false)
	}
	if err != nil {
		return err
	}
	store.release = func(ctx context.Context) error { return store.docker.ContainerDelete(ctx, id) }
	return nil
}

// busy says since when another nemo holds the stack.
func (store *Store) busy(ctx context.Context, name string, config bool) error {
	since, free := "a while", "docker rm "+name
	if config {
		free = "docker config rm " + name
		if holder, err := store.docker.API().ConfigInspect(ctx, name, client.ConfigInspectOptions{}); err == nil {
			since = holder.Config.CreatedAt.Local().Format(time.DateTime)
			if host := strings.TrimSpace(string(holder.Config.Spec.Data)); host != "" {
				since += ", from " + host
			}
		}
	} else if holder, err := store.docker.ContainerInspect(ctx, name); err == nil {
		if created, err := time.Parse(time.RFC3339Nano, holder.Created); err == nil {
			since = created.Local().Format(time.DateTime)
		}
	}
	return fmt.Errorf("stack %s is %w: another nemo writes it since %s; if none runs, %s on the target frees it", store.stack, ErrBusy, since, free)
}

// attach creates the container that reaches the volume, and copies the
// records out of it.
func (store *Store) attach(ctx context.Context) error {
	id, err := store.docker.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:   Name(store.stack) + "-" + strings.ToLower(rand.Text()[:8]),
		Config: &container.Config{Image: Image, Cmd: []string{"records"}, Labels: map[string]string{Label: store.stack}},
		HostConfig: &container.HostConfig{
			Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: Name(store.stack), Target: mountPoint}},
		},
	})
	if err != nil {
		return err
	}
	store.container = id
	return store.copyOut(ctx)
}

// detach removes the container that reaches the volume.
func (store *Store) detach(ctx context.Context) error {
	id := store.container
	store.container = ""
	return store.docker.ContainerDelete(context.WithoutCancel(ctx), id)
}

// move brings the records of a volume that is not of the records type to a
// volume of it: it copies them out, recreates the volume and copies them
// in, unless the type's storage has records of the stack already, which
// another Docker moved there. Until they are in, a local copy keeps them,
// which an error names.
func (store *Store) move(ctx context.Context, spec *volume.Volume, provision *target.VolumeProvision) error {
	if err := store.attach(ctx); err != nil {
		return err
	}
	kept, owner := store.mirror, store.owner
	store.mirror, store.local = "", nil
	keptIn := func(err error) error {
		return fmt.Errorf("moving the records of %s to the volume type %s: %w; they are kept in %s", store.stack, Type, err, filepath.Join(kept, store.stack))
	}
	if err := store.detach(ctx); err != nil {
		return keptIn(err)
	}
	if err := store.docker.VolumeDelete(ctx, spec); err != nil {
		return errors.Join(err, os.RemoveAll(kept))
	}
	if err := store.createVolume(ctx, spec, provision); err != nil {
		return keptIn(err)
	}
	if err := store.attach(ctx); err != nil {
		return keptIn(err)
	}
	nothing, err := isEmpty(filepath.Join(kept, store.stack))
	if err != nil || nothing {
		return errors.Join(err, os.RemoveAll(kept))
	}
	fresh, err := isEmpty(filepath.Join(store.mirror, store.stack))
	if err != nil {
		return keptIn(err)
	}
	if !fresh {
		return keptIn(fmt.Errorf("the storage of the type has records of the stack already"))
	}
	if err := os.RemoveAll(store.mirror); err != nil {
		return keptIn(err)
	}
	store.mirror, store.local, store.owner = kept, file.New(kept), owner
	if err := store.copyIn(ctx, "."); err != nil {
		store.mirror = ""
		return keptIn(err)
	}
	return nil
}

func isEmpty(directory string) (bool, error) {
	entries, err := os.ReadDir(directory)
	return len(entries) == 0, err
}

// copyOut copies the volume into a local directory, the stack's directory
// of a file store.
func (store *Store) copyOut(ctx context.Context) error {
	mirror, err := os.MkdirTemp("", "nemo-records-")
	if err != nil {
		return err
	}
	store.mirror, store.local = mirror, file.New(mirror)
	// A stack without records yet has its directory all the same, for its owner.
	if err := os.Mkdir(filepath.Join(mirror, store.stack), 0o700); err != nil {
		return err
	}
	content, err := store.docker.ContainerCopyFrom(ctx, store.container, mountPoint)
	if err != nil {
		return err
	}
	defer content.Close()
	if err := extract(content, filepath.Join(mirror, store.stack)); err != nil {
		return fmt.Errorf("reading the records of %s: %w", store.stack, err)
	}
	owner, err := os.ReadFile(filepath.Join(mirror, store.stack, ownerFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	store.owner = strings.TrimSpace(string(owner))
	return nil
}

// extract writes the archive of the volume in directory: its entries, under
// the volume's own directory, are directories and files, inside it only.
func extract(archive io.Reader, directory string) error {
	entries := tar.NewReader(archive)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		root, name, _ := strings.Cut(path.Clean(header.Name), "/")
		if root != path.Base(mountPoint) || name != "" && !fs.ValidPath(name) {
			return fmt.Errorf("%s: not a path in the volume", header.Name)
		}
		if name == "" {
			continue
		}
		target := filepath.Join(directory, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			data, err := io.ReadAll(entries)
			if err != nil {
				return err
			}
			if err := os.WriteFile(target, data, 0o600); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: neither a file nor a directory", header.Name)
		}
	}
}

// push copies the installation's directory back to the volume.
func (store *Store) push(ctx context.Context, name string) error {
	return store.copyIn(ctx, filepath.Join("installations", name))
}

// copyIn copies a file or a directory of the mirror, at its path in the
// volume.
func (store *Store) copyIn(ctx context.Context, relative string) error {
	var content bytes.Buffer
	archive := tar.NewWriter(&content)
	root := filepath.Join(store.mirror, store.stack)
	err := filepath.WalkDir(filepath.Join(root, relative), func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, current)
		if err != nil || name == "." {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(name)
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		_, err = archive.Write(data)
		return err
	})
	if err != nil {
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if _, err := store.docker.API().CopyToContainer(ctx, store.container, client.CopyToContainerOptions{DestinationPath: mountPoint, Content: &content}); err != nil {
		return fmt.Errorf("writing the records of %s: %w", store.stack, err)
	}
	return nil
}

// Close removes the container, frees the stack, and removes the local copy.
func (store *Store) Close(ctx context.Context) error {
	ctx = context.WithoutCancel(ctx)
	var errs []error
	if store.container != "" {
		errs = append(errs, store.detach(ctx))
	}
	if store.release != nil {
		errs = append(errs, store.release(ctx))
		store.release = nil
	}
	if store.mirror != "" {
		errs = append(errs, os.RemoveAll(store.mirror))
	}
	return errors.Join(errs...)
}

// Owner is who manages the stack: empty for nemo, else the tool that set it.
func (store *Store) Owner() string {
	return store.owner
}

// SetOwner records who manages the stack.
func (store *Store) SetOwner(ctx context.Context, owner string) error {
	if err := os.WriteFile(filepath.Join(store.mirror, store.stack, ownerFile), []byte(owner+"\n"), 0o600); err != nil {
		return err
	}
	store.owner = owner
	return store.copyIn(ctx, ownerFile)
}

// check refuses another stack's records: the store holds one stack's.
func (store *Store) check(stack string) error {
	if stack != store.stack {
		return fmt.Errorf("the records of %s are not those of %s", store.stack, stack)
	}
	return nil
}

func (store *Store) GetInstallation(ctx context.Context, stack, name string) (storage.Installation, error) {
	if err := store.check(stack); err != nil {
		return storage.Installation{}, err
	}
	return store.local.GetInstallation(ctx, stack, name)
}

func (store *Store) ListInstallations(ctx context.Context, stack string) ([]storage.Installation, error) {
	if err := store.check(stack); err != nil {
		return nil, err
	}
	return store.local.ListInstallations(ctx, stack)
}

func (store *Store) SaveInstallation(ctx context.Context, installation storage.Installation) error {
	if err := store.check(installation.Stack); err != nil {
		return err
	}
	if err := store.local.SaveInstallation(ctx, installation); err != nil {
		return err
	}
	return store.push(ctx, installation.Name)
}

func (store *Store) SaveRun(ctx context.Context, stack, name string, run cnab.Claim) error {
	if err := store.check(stack); err != nil {
		return err
	}
	if err := store.local.SaveRun(ctx, stack, name, run); err != nil {
		return err
	}
	return store.push(ctx, name)
}

func (store *Store) SaveResult(ctx context.Context, stack, name string, result cnab.Result, outputs map[string]string) error {
	if err := store.check(stack); err != nil {
		return err
	}
	if err := store.local.SaveResult(ctx, stack, name, result, outputs); err != nil {
		return err
	}
	return store.push(ctx, name)
}

func (store *Store) GetRun(ctx context.Context, stack, name, id string) (cnab.Claim, error) {
	if err := store.check(stack); err != nil {
		return cnab.Claim{}, err
	}
	return store.local.GetRun(ctx, stack, name, id)
}

func (store *Store) ListRuns(ctx context.Context, stack, name string) ([]cnab.Claim, error) {
	if err := store.check(stack); err != nil {
		return nil, err
	}
	return store.local.ListRuns(ctx, stack, name)
}

func (store *Store) ListResults(ctx context.Context, stack, name string) ([]cnab.Result, error) {
	if err := store.check(stack); err != nil {
		return nil, err
	}
	return store.local.ListResults(ctx, stack, name)
}

func (store *Store) GetOutputs(ctx context.Context, stack, name, resultID string) (map[string]string, error) {
	if err := store.check(stack); err != nil {
		return nil, err
	}
	return store.local.GetOutputs(ctx, stack, name, resultID)
}
