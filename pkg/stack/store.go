package stack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/sntns/nemo/pkg/storage"
)

// Formats of a manifest, which are also its extension.
const (
	YAML = "yaml"
	JSON = "json"
)

// Store keeps the stacks of a home by their name, each its manifest in a
// directory of its own, <name>/manifest.yaml or manifest.json, where its
// installations are kept too, and which one is current. A directory of
// the home without a manifest is no stack: the home can hold other files,
// those of a tool built on nemo.
type Store struct {
	root string
}

// NewStore returns the store of the home root.
func NewStore(root string) *Store {
	return &Store{root: root}
}

func (store *Store) directory(name string) (string, error) {
	if !stackName.MatchString(name) || name == "." || name == ".." || name == "current" {
		return "", fmt.Errorf("stack %q: not a stack name", name)
	}
	return filepath.Join(store.root, name), nil
}

// Path is the stack's manifest, in the format it was written in.
func (store *Store) Path(name string) (string, error) {
	directory, err := store.directory(name)
	if err != nil {
		return "", err
	}
	for _, format := range []string{YAML, JSON} {
		path := filepath.Join(directory, "manifest."+format)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("stack %s: %w", name, storage.ErrNotFound)
}

// Load reads the stack of that name.
func (store *Store) Load(name string) (Stack, error) {
	path, err := store.Path(name)
	if err != nil {
		return Stack{}, err
	}
	return Load(path)
}

// Save writes the stack's manifest in format, or else in the one it has
// already, YAML for a new stack.
func (store *Store) Save(stack Stack, format string) error {
	if err := stack.Validate(); err != nil {
		return err
	}
	directory, err := store.directory(stack.Name)
	if err != nil {
		return err
	}
	existing, err := store.Path(stack.Name)
	if format == "" {
		format = YAML
		if err == nil {
			format = strings.TrimPrefix(filepath.Ext(existing), ".")
		}
	}
	data, err := Encode(stack, format)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	path := filepath.Join(directory, "manifest."+format)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if existing != "" && existing != path {
		return os.Remove(existing)
	}
	return nil
}

// List returns the names of the stacks.
func (store *Store) List() ([]string, error) {
	entries, err := os.ReadDir(store.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if _, err := store.Path(entry.Name()); entry.IsDir() && err == nil {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// Current returns the name of the current stack, empty when there is none.
func (store *Store) Current() (string, error) {
	data, err := os.ReadFile(filepath.Join(store.root, "current"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(data)), err
}

// Use makes the stack the current one.
func (store *Store) Use(name string) error {
	if _, err := store.Path(name); err != nil {
		return err
	}
	if err := os.MkdirAll(store.root, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(store.root, "current"), []byte(name+"\n"), 0o600)
}

// Remove removes the stack's directory, its manifest and the records of
// its installations, and makes no stack current if it was.
func (store *Store) Remove(name string) error {
	path, err := store.Path(name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		return err
	}
	if current, err := store.Current(); err != nil || current != name {
		return err
	}
	return os.Remove(filepath.Join(store.root, "current"))
}

// Encode writes the stack as a manifest in format. Its installations give
// no stack: they are the manifest's.
func Encode(stack Stack, format string) ([]byte, error) {
	stack.Installations = slices.Clone(stack.Installations)
	for i := range stack.Installations {
		stack.Installations[i].Stack = ""
	}
	data, err := json.MarshalIndent(stack, "", "  ")
	if err != nil || format == JSON {
		return append(data, '\n'), err
	}
	if format != YAML {
		return nil, fmt.Errorf("format %s: expected %s or %s", format, YAML, JSON)
	}
	// Read as YAML, the JSON keeps its order; written in block style, as people write YAML.
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	var block func(node *yaml.Node)
	block = func(node *yaml.Node) {
		node.Style = 0
		for _, child := range node.Content {
			block(child)
		}
	}
	block(&document)
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, err
	}
	return buffer.Bytes(), encoder.Close()
}
