package cnab

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/Masterminds/semver/v3"
	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/bundle/definition"
	"github.com/cnabio/cnab-go/claim"
)

// SupportedVersion is the range of bundle schema versions nemo reads.
const SupportedVersion = "1.0.0 || 1.1.0 || 1.2.0"

// ExtendedBundle is a bundle that passed nemo's checks, with the extensions
// nemo knows decoded from its custom section. Build it with NewBundle,
// LoadBundle or UnmarshalBundle, never as a literal.
//
// The decoded extensions are views of Custom: to change one, write it with
// its Extension and build the ExtendedBundle again.
type ExtendedBundle struct {
	bundle.Bundle

	// Each extension is nil when the bundle does not carry it.
	Docker       *Docker
	Dependencies *Dependencies
	Compose      *Compose
}

// NewBundle validates a bundle and decodes its extensions.
func NewBundle(bundle bundle.Bundle) (ExtendedBundle, error) {
	extendedBundle := ExtendedBundle{Bundle: bundle}
	if err := extendedBundle.validate(); err != nil {
		return ExtendedBundle{}, err
	}

	if err := errors.Join(
		read(DockerExtension, bundle, &extendedBundle.Docker),
		read(DependenciesExtension, bundle, &extendedBundle.Dependencies),
		read(ComposeExtension, bundle, &extendedBundle.Compose),
	); err != nil {
		return ExtendedBundle{}, fmt.Errorf("invalid bundle: %w", err)
	}
	return extendedBundle, nil
}

// UnmarshalBundle reads a bundle.json.
func UnmarshalBundle(data []byte) (ExtendedBundle, error) {
	cnabBundle, err := bundle.Unmarshal(data)
	if err != nil {
		return ExtendedBundle{}, fmt.Errorf("cannot decode bundle: %w", err)
	}
	return NewBundle(*cnabBundle)
}

// LoadBundle reads a bundle.json from the specified filepath.
func LoadBundle(bundleFile string) (ExtendedBundle, error) {
	data, err := os.ReadFile(bundleFile)
	if err != nil {
		return ExtendedBundle{}, fmt.Errorf("cannot read bundle at %s: %w", bundleFile, err)
	}
	bundle, err := UnmarshalBundle(data)
	if err != nil {
		return ExtendedBundle{}, fmt.Errorf("%s: %w", bundleFile, err)
	}
	return bundle, nil
}

// validate checks the bundle itself and its schema version.
//
// cnab-go requires every required extension to have an entry in custom, even
// an empty one: a nemo bundle carries "io.cnab.docker": {}.
func (bundle ExtendedBundle) validate() error {
	if err := bundle.Validate(); err != nil {
		return fmt.Errorf("invalid bundle: %w", err)
	}

	supported, err := semver.NewConstraint(SupportedVersion)
	if err != nil {
		return fmt.Errorf("invalid supported version %s: %w", SupportedVersion, err)
	}
	version, err := semver.NewVersion(string(bundle.SchemaVersion))
	if err != nil {
		return fmt.Errorf("invalid bundle schema version %q: %w", bundle.SchemaVersion, err)
	}
	if !supported.Check(version) {
		return fmt.Errorf("unsupported bundle schema version %s: supported versions are %s", bundle.SchemaVersion, SupportedVersion)
	}
	return nil
}

// IsSensitiveParameter determines if the parameter contains a sensitive value.
func (bundle ExtendedBundle) IsSensitiveParameter(name string) bool {
	if parameter, exists := bundle.Parameters[name]; exists {
		if definition, exists := bundle.Definitions[parameter.Definition]; exists {
			return definition.WriteOnly != nil && *definition.WriteOnly
		}
	}
	return false
}

// GetParameterType determines the type of parameter, accounting for files.
func (bundle ExtendedBundle) GetParameterType(definition *definition.Schema) string {
	if bundle.IsFileType(definition) {
		return "file"
	}

	if definition.ID == claim.OutputInvocationImageLogs {
		return "string"
	}

	return fmt.Sprintf("%v", definition.Type)
}

// IsFileType determines if the parameter/credential is of type "file":
// a base64-encoded string, delivered to the installer as a file.
func (bundle ExtendedBundle) IsFileType(definition *definition.Schema) bool {
	return definition.Type == "string" && definition.ContentEncoding == "base64"
}

// ConvertParameterValue converts a parameter's value from an unknown type,
// it could be a string from stdin or another Go type, into the type of the
// parameter as defined in the bundle.
func (bundle ExtendedBundle) ConvertParameterValue(key string, value any) (any, error) {
	parameter, ok := bundle.Parameters[key]
	if !ok {
		return nil, fmt.Errorf("unable to convert the parameters' value to the destination parameter type because parameter %s not defined in bundle", key)
	}

	definition, ok := bundle.Definitions[parameter.Definition]
	if !ok {
		return nil, fmt.Errorf("unable to convert the parameters' value to the destination parameter type because parameter %s has no definition", key)
	}

	if definition.Type != nil {
		switch t := value.(type) {
		case string:
			typedValue, err := definition.ConvertValue(t)
			if err != nil {
				return nil, fmt.Errorf("unable to convert parameter's %s value %s to the destination parameter type %s: %w", key, value, definition.Type, err)
			}
			return typedValue, nil
		case json.Number:
			switch definition.Type {
			case "integer":
				return t.Int64()
			case "number":
				return t.Float64()
			default:
				return t.String(), nil
			}
		default:
			return t, nil
		}
	} else {
		return value, nil
	}
}

func (bundle ExtendedBundle) WriteParameterToString(parameterName string, value any) (string, error) {
	return WriteParameterToString(parameterName, value)
}

// WriteParameterToString changes a parameter's value from its type as
// defined by the bundle to its runtime string representation.
// The value should have already been converted to its bundle representation
// by calling ConvertParameterValue.
func WriteParameterToString(parameterName string, value any) (string, error) {
	if value == nil {
		return "", nil
	}

	if stringValue, ok := value.(string); ok {
		return stringValue, nil
	}

	contents, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("could not marshal the value for parameter %s to a json string %#v: %w", parameterName, value, err)
	}

	return string(contents), nil
}

// GetReferencedRegistries identifies all OCI registries used by the bundle
// from both the bundle image and the referenced images.
func (bundle ExtendedBundle) GetReferencedRegistries() ([]string, error) {
	registrySet := make(map[string]struct{})
	for _, invocationImage := range bundle.InvocationImages {
		imageReference, err := ParseOCIReference(invocationImage.Image)
		if err != nil {
			return nil, fmt.Errorf("could not parse the bundle image %s as an OCI image reference: %w", invocationImage.Image, err)
		}

		registrySet[imageReference.Registry()] = struct{}{}
	}

	for key, image := range bundle.Images {
		imageReference, err := ParseOCIReference(image.Image)
		if err != nil {
			return nil, fmt.Errorf("could not parse the referenced image %s (%s) as an OCI image reference: %w", image.Image, key, err)
		}
		registrySet[imageReference.Registry()] = struct{}{}
	}

	registries := make([]string, 0, len(registrySet))
	for registry := range registrySet {
		registries = append(registries, registry)
	}
	sort.Strings(registries)
	return registries, nil
}
