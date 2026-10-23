// Package file is a store in a local directory, one per installation, in
// the directory of its stack, next to the stack's manifest:
//
//	<root>/<stack>/installations/<name>/installation.json
//	<root>/<stack>/installations/<name>/runs/<claim id>.json
//	<root>/<stack>/installations/<name>/results/<result id>.json
//	<root>/<stack>/installations/<name>/outputs/<result id>/<output>
//
// Files are written whole, through a temporary file, and readable by their
// owner only.
package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/storage"
)

// HomeVariable overrides where nemo keeps its files.
const HomeVariable = "NEMO_HOME"

// Store is a store in a directory.
type Store struct {
	root string
}

var _ storage.Store = (*Store)(nil)

// New returns the store in root.
func New(root string) *Store {
	return &Store{root: root}
}

// DefaultRoot is $NEMO_HOME, or ~/.nemo.
func DefaultRoot() (string, error) {
	if home := os.Getenv(HomeVariable); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding where to keep nemo's files: %w", err)
	}
	return filepath.Join(home, ".nemo"), nil
}

func (store *Store) GetInstallation(ctx context.Context, stack, name string) (storage.Installation, error) {
	directory, err := store.directory(stack, name)
	if err != nil {
		return storage.Installation{}, err
	}
	var installation storage.Installation
	switch err := read(filepath.Join(directory, "installation.json"), &installation); {
	case errors.Is(err, fs.ErrNotExist):
		return storage.Installation{}, fmt.Errorf("installation %s/%s: %w", stack, name, storage.ErrNotFound)
	case err != nil:
		return storage.Installation{}, err
	case installation.SchemaVersion != storage.SchemaVersion:
		return storage.Installation{}, fmt.Errorf("installation %s/%s: schema %s, this nemo reads %s: uninstall it with the nemo that wrote it, or remove %s", stack, name, installation.SchemaVersion, storage.SchemaVersion, directory)
	}
	return installation, nil
}

func (store *Store) ListInstallations(ctx context.Context, stack string) ([]storage.Installation, error) {
	if !safe(stack) {
		return nil, fmt.Errorf("stack %s: the name can't name a directory", stack)
	}
	entries, err := os.ReadDir(filepath.Join(store.root, stack, "installations"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var installations []storage.Installation
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		installation, err := store.GetInstallation(ctx, stack, entry.Name())
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		installations = append(installations, installation)
	}
	return installations, nil
}

func (store *Store) SaveInstallation(ctx context.Context, installation storage.Installation) error {
	directory, err := store.directory(installation.Stack, installation.Name)
	if err != nil {
		return err
	}
	installation.SchemaVersion = storage.SchemaVersion
	return write(filepath.Join(directory, "installation.json"), installation)
}

// SaveRun writes the claim, named after its ID.
func (store *Store) SaveRun(ctx context.Context, stack, name string, run cnab.Claim) error {
	directory, err := store.directory(stack, name)
	if err != nil {
		return err
	}
	if !safe(run.ID) {
		return fmt.Errorf("run of %s/%s: ID %q can't name a file", stack, name, run.ID)
	}
	return write(filepath.Join(directory, "runs", run.ID+".json"), run)
}

// SaveResult writes the result, named after its ID, and each output in a
// file of its own.
func (store *Store) SaveResult(ctx context.Context, stack, name string, result cnab.Result, outputs map[string]string) error {
	directory, err := store.directory(stack, name)
	if err != nil {
		return err
	}
	if !safe(result.ID) {
		return fmt.Errorf("result of %s/%s: ID %q can't name a file", stack, name, result.ID)
	}
	if err := write(filepath.Join(directory, "results", result.ID+".json"), result); err != nil {
		return err
	}
	for output, value := range outputs {
		if !safe(output) {
			return fmt.Errorf("result of %s/%s: output %q can't name a file", stack, name, output)
		}
		if err := writeBytes(filepath.Join(directory, "outputs", result.ID, output), []byte(value)); err != nil {
			return err
		}
	}
	return nil
}

// GetRun reads the claim of that ID.
func (store *Store) GetRun(ctx context.Context, stack, name, id string) (cnab.Claim, error) {
	directory, err := store.directory(stack, name)
	if err != nil {
		return cnab.Claim{}, err
	}
	if !safe(id) {
		return cnab.Claim{}, fmt.Errorf("run of %s/%s: ID %q can't name a file", stack, name, id)
	}
	var run cnab.Claim
	switch err := read(filepath.Join(directory, "runs", id+".json"), &run); {
	case errors.Is(err, fs.ErrNotExist):
		return cnab.Claim{}, fmt.Errorf("run %s of %s/%s: %w", id, stack, name, storage.ErrNotFound)
	case err != nil:
		return cnab.Claim{}, err
	}
	return run, nil
}

// ListRuns returns the claims sorted by ID: claim IDs are ULIDs, which sort
// by time.
func (store *Store) ListRuns(ctx context.Context, stack, name string) ([]cnab.Claim, error) {
	return list[cnab.Claim](store, stack, name, "runs")
}

// ListResults returns the results sorted by ID, ULIDs too.
func (store *Store) ListResults(ctx context.Context, stack, name string) ([]cnab.Result, error) {
	return list[cnab.Result](store, stack, name, "results")
}

// list reads the documents of a directory of the installation, by name.
func list[T any](store *Store, stack, name, kind string) ([]T, error) {
	directory, err := store.directory(stack, name)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(directory, kind))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)
	documents := make([]T, 0, len(names))
	for _, file := range names {
		var document T
		if err := read(filepath.Join(directory, kind, file), &document); err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, nil
}

// GetOutputs reads the files a result's outputs were written to.
func (store *Store) GetOutputs(ctx context.Context, stack, name, resultID string) (map[string]string, error) {
	directory, err := store.directory(stack, name)
	if err != nil {
		return nil, err
	}
	if !safe(resultID) {
		return nil, fmt.Errorf("result of %s/%s: ID %q can't name a file", stack, name, resultID)
	}
	entries, err := os.ReadDir(filepath.Join(directory, "outputs", resultID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	outputs := map[string]string{}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(directory, "outputs", resultID, entry.Name()))
		if err != nil {
			return nil, err
		}
		outputs[entry.Name()] = string(data)
	}
	return outputs, nil
}

// directory is the directory of an installation. Stack and bundle names
// come from users and bundles: they must not reach outside the root.
func (store *Store) directory(stack, name string) (string, error) {
	if !safe(stack) || !safe(name) {
		return "", fmt.Errorf("installation %s/%s: the names can't name a directory", stack, name)
	}
	return filepath.Join(store.root, stack, "installations", name), nil
}

func safe(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

func read(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	return nil
}

// write writes value as JSON, through writeBytes.
func write(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeBytes(path, append(data, '\n'))
}

// writeBytes replaces the file whole: a reader sees the old content or the
// new one, never a part.
func writeBytes(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
