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
// The container of a store held for writing is named after the stack:
// another can't be created while it exists, so a second nemo writing the
// stack finds it busy.
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
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/jsonmessage"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/storage/file"
)

const (
	// Label names the stack whose records a volume or a container holds.
	// It is not the stack's own label, which uninstalling a stack selects.
	Label = "io.nemo.records"
	// Image is the empty image of the containers that reach the volumes.
	Image = "nemo-records:1"
	// mountPoint is where the volume is in the container.
	mountPoint = "/records"
	// ownerFile names who manages the stack, when not nemo itself.
	ownerFile = "owner"
)

// ErrBusy is returned when another nemo holds the stack for writing.
var ErrBusy = errors.New("busy")

// Name is the name of the volume, and of the container that holds it.
func Name(stack string) string {
	return "nemo-records-" + stack
}

var dockerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// Store is the store of one stack, from Open to Close.
type Store struct {
	docker    *docker.Client
	stack     string
	container string
	mirror    string
	local     *file.Store
	owner     string
}

var _ storage.Store = (*Store)(nil)

// Open copies the records of the stack from its volume, which it creates the
// first time. Held, it is the stack's only writer until Close: another
// Open held fails with ErrBusy.
func Open(ctx context.Context, api client.APIClient, stack string, held bool) (*Store, error) {
	if !dockerName.MatchString(stack) {
		return nil, fmt.Errorf("stack %s: its records are kept on the target in a volume named after it, which Docker refuses: letters, digits, _ . and - only", stack)
	}
	store := &Store{docker: docker.New(api), stack: stack}
	if err := store.ensureImage(ctx); err != nil {
		return nil, err
	}
	if err := store.ensureVolume(ctx); err != nil {
		return nil, err
	}
	if err := store.createContainer(ctx, held); err != nil {
		return nil, err
	}
	if err := store.copyOut(ctx); err != nil {
		_ = store.Close(ctx)
		return nil, err
	}
	return store, nil
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

func (store *Store) ensureVolume(ctx context.Context) error {
	existing, err := store.docker.VolumeFind(ctx, Name(store.stack))
	if err != nil || existing != nil {
		return err
	}
	return store.docker.VolumeCreate(ctx, &volume.Volume{Name: Name(store.stack), Labels: map[string]string{Label: store.stack}})
}

func (store *Store) createContainer(ctx context.Context, held bool) error {
	name := Name(store.stack)
	if !held {
		name += "-" + strings.ToLower(rand.Text()[:8])
	}
	id, err := store.docker.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:   name,
		Config: &container.Config{Image: Image, Cmd: []string{"records"}, Labels: map[string]string{Label: store.stack}},
		HostConfig: &container.HostConfig{
			Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: Name(store.stack), Target: mountPoint}},
		},
	})
	if held && errdefs.IsConflict(err) {
		return store.busy(ctx, name)
	}
	if err != nil {
		return err
	}
	store.container = id
	return nil
}

// busy says since when another nemo holds the stack.
func (store *Store) busy(ctx context.Context, name string) error {
	since := "a while"
	if holder, err := store.docker.ContainerInspect(ctx, name); err == nil {
		if created, err := time.Parse(time.RFC3339Nano, holder.Created); err == nil {
			since = created.Local().Format(time.DateTime)
		}
	}
	return fmt.Errorf("stack %s is %w: another nemo writes it since %s; if none runs, docker rm %s on the target frees it", store.stack, ErrBusy, since, name)
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
		if err != nil {
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

// Close removes the container, which frees the stack, and the local copy.
func (store *Store) Close(ctx context.Context) error {
	ctx = context.WithoutCancel(ctx)
	var errs []error
	if store.container != "" {
		errs = append(errs, store.docker.ContainerDelete(ctx, store.container))
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
