package cnab

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/cnabio/cnab-go/bundle"
)

const (
	// OfficialExtensionsPrefix is the prefix applied to extensions defined in the CNAB spec.
	OfficialExtensionsPrefix = "io.cnab."

	// NemoExtensionsPrefix is the prefix applied to nemo's own extensions.
	NemoExtensionsPrefix = "io.nemo."
)

// ErrExtensionNotFound is returned when reading an extension the bundle does not carry.
var ErrExtensionNotFound = errors.New("extension not found")

// Extension is a custom extension of a bundle: a key in custom, a JSON schema
// embedded in schemas/, and the Go type its value decodes to.
//
// Whether a bundle may list it in requiredExtensions is not a property of the
// extension: see RuntimeExtensions.
type Extension[T any] struct {
	// Key is the key of the extension in custom.
	Key string
	// Shorthand is the short name some bundles require the extension by.
	// https://github.com/cnabio/cnab-spec/issues/403
	Shorthand string
	// Schema is the file of the extension's schema in schemas/.
	Schema string
}

// Name returns the key of the extension.
func (extension Extension[T]) Name() string { return extension.Key }

// Matches reports whether a name in requiredExtensions designates the extension.
func (extension Extension[T]) Matches(name string) bool {
	return name == extension.Key || (extension.Shorthand != "" && name == extension.Shorthand)
}

// RequiredBy reports whether the bundle lists the extension in requiredExtensions.
func (extension Extension[T]) RequiredBy(bundle bundle.Bundle) bool {
	return slices.ContainsFunc(bundle.RequiredExtensions, extension.Matches)
}

// Read decodes the extension from the bundle's custom section, after checking
// it against its schema. It returns ErrExtensionNotFound when the bundle does
// not carry it.
func (extension Extension[T]) Read(bundle bundle.Bundle) (T, error) {
	var value T
	data, ok := bundle.Custom[extension.Key]
	if !ok {
		return value, fmt.Errorf("%s: %w", extension.Key, ErrExtensionNotFound)
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return value, fmt.Errorf("%s: %w", extension.Key, err)
	}
	if err := validateSchema(extension.Schema, raw); err != nil {
		return value, fmt.Errorf("%s: %w", extension.Key, err)
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("%s: %w", extension.Key, err)
	}
	return value, nil
}

// Write stores the extension in the bundle's custom section, after checking
// it against its schema.
func (extension Extension[T]) Write(bundle *bundle.Bundle, value T) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%s: %w", extension.Key, err)
	}
	if err := validateSchema(extension.Schema, raw); err != nil {
		return fmt.Errorf("%s: %w", extension.Key, err)
	}
	if bundle.Custom == nil {
		bundle.Custom = map[string]any{}
	}
	bundle.Custom[extension.Key] = json.RawMessage(raw)
	return nil
}

// read decodes the extension into *dst when the bundle carries it, and leaves
// *dst untouched otherwise.
func read[T any](extension Extension[T], bundle bundle.Bundle, destination **T) error {
	if _, ok := bundle.Custom[extension.Key]; !ok {
		return nil
	}
	value, err := extension.Read(bundle)
	if err != nil {
		return err
	}
	*destination = &value
	return nil
}

// ExtensionDefinition is an Extension, whatever the type of its value.
type ExtensionDefinition interface {
	Name() string
	Matches(name string) bool
}

var (
	// RuntimeExtensions are the extensions the nemo runtime honours when a
	// bundle requires them.
	RuntimeExtensions = []ExtensionDefinition{
		DependenciesExtension,
		DockerExtension,
	}

	// InstallerExtensions are read by the installer, never by the runtime.
	// A bundle must not require them: a CNAB runtime refuses a bundle that
	// requires an extension it does not know, and Porter does.
	InstallerExtensions = []ExtensionDefinition{
		ComposeExtension,
	}
)

// CheckRequiredExtensions checks that the nemo runtime honours every
// extension the bundle requires.
func (bundle ExtendedBundle) CheckRequiredExtensions() error {
	for _, name := range bundle.RequiredExtensions {
		if slices.ContainsFunc(RuntimeExtensions, matching(name)) {
			continue
		}
		if slices.ContainsFunc(InstallerExtensions, matching(name)) {
			return fmt.Errorf("%s must not be listed in requiredExtensions: it is read by the installer, and other CNAB runtimes would refuse the bundle", name)
		}
		return fmt.Errorf("unsupported required extension: %s", name)
	}
	return nil
}

func matching(name string) func(ExtensionDefinition) bool {
	return func(extension ExtensionDefinition) bool { return extension.Matches(name) }
}
