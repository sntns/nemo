package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-to-oci/relocation"

	"github.com/sntns/nemo/pkg/cnab"
)

// Locations and variables of the CNAB contract between a runtime and its
// invocation image (cnab-spec 103-bundle-runtime.md).
const (
	BundleFile          = "/cnab/bundle.json"
	EnvAction           = "CNAB_ACTION"
	EnvInstallationName = "CNAB_INSTALLATION_NAME"
	EnvBundleName       = "CNAB_BUNDLE_NAME"
	EnvRevision         = "CNAB_REVISION"
)

// Operation is what a CNAB runtime hands to the invocation image, seen from
// inside the container. It is the counterpart of cnab-go's driver.Operation:
// cnab-go based runtimes, Porter and nemo among them, build one from a claim
// and turn it into these variables and files.
type Operation struct {
	Installation string
	Revision     string
	Action       string
	Bundle       cnab.ExtendedBundle

	// Parameters holds every parameter that applies to the action, typed by
	// its definition: the value the runtime delivered, else the default.
	Parameters map[string]any

	// Credentials holds every credential the runtime delivered for the action.
	Credentials map[string]string

	// Relocation maps the bundle's image references to where the images are
	// on the target. It is empty when the images were not relocated.
	Relocation relocation.ImageRelocationMap

	// container is where the operation was read from: the compose project
	// reads the files its configs and secrets name from it.
	container Container
}

// Container is what the invocation image sees of the operation: its
// environment variables and its filesystem.
type Container struct {
	LookupEnv func(key string) (string, bool)
	// FS is the container's filesystem, rooted at "/".
	FS fs.FS
}

// ReadOperation reads the operation the runtime handed to the container.
func ReadOperation(container Container) (Operation, error) {
	data, err := container.readFile(BundleFile)
	if err != nil {
		return Operation{}, err
	}

	bundle, err := cnab.UnmarshalBundle(data)
	if err != nil {
		return Operation{}, fmt.Errorf("%s: %w", BundleFile, err)
	}

	operation := Operation{Bundle: bundle, container: container}
	operation.Action, _ = container.LookupEnv(EnvAction)
	operation.Installation, _ = container.LookupEnv(EnvInstallationName)
	operation.Revision, _ = container.LookupEnv(EnvRevision)

	var errs []error
	if operation.Action == "" {
		errs = append(errs, fmt.Errorf("%s is not set", EnvAction))
	}
	if operation.Installation == "" {
		errs = append(errs, fmt.Errorf("%s is not set", EnvInstallationName))
	}
	if name, _ := container.LookupEnv(EnvBundleName); name != "" && name != bundle.Name {
		errs = append(errs, fmt.Errorf("%s is %s, but the bundle is %s", EnvBundleName, name, bundle.Name))
	}
	if len(errs) > 0 {
		return Operation{}, errors.Join(errs...)
	}

	if operation.Parameters, err = container.readParameters(bundle, operation.Action); err != nil {
		return Operation{}, err
	}
	if operation.Credentials, err = container.readCredentials(bundle, operation.Action); err != nil {
		return Operation{}, err
	}
	if operation.Relocation, err = container.readRelocation(); err != nil {
		return Operation{}, err
	}

	return operation, nil
}

// readParameters reads every parameter that applies to the action from its
// destination, and converts it to its definition's type.
func (container Container) readParameters(bundle cnab.ExtendedBundle, action string) (map[string]any, error) {
	values := map[string]any{}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(bundle.Parameters)) {
		parameter := bundle.Parameters[name]
		if !parameter.AppliesTo(action) {
			continue
		}

		definition, ok := bundle.Definitions[parameter.Definition]
		if !ok {
			errs = append(errs, fmt.Errorf("parameter %s: no definition %s", name, parameter.Definition))
			continue
		}

		raw, delivered, err := container.deliver(parameter.Destination)
		// Porter delivers a parameter it was not given empty: of a type
		// that has no empty value, it is not given.
		if delivered && raw == "" && definition.Type != "string" {
			delivered = false
		}
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("parameter %s: %w", name, err))
		case delivered:
			value, err := definition.ConvertValue(raw)
			if err != nil {
				errs = append(errs, fmt.Errorf("parameter %s: %w", name, err))
				continue
			}
			values[name] = value
		case definition.Default != nil:
			values[name] = definition.Default
		case parameter.Required:
			errs = append(errs, fmt.Errorf("parameter %s is required for %s but was not provided", name, action))
		}
	}
	return values, errors.Join(errs...)
}

// readCredentials reads every credential that applies to the action.
func (container Container) readCredentials(bundle cnab.ExtendedBundle, action string) (map[string]string, error) {
	values := map[string]string{}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(bundle.Credentials)) {
		credential := bundle.Credentials[name]
		if !credential.AppliesTo(action) {
			continue
		}

		value, delivered, err := container.deliver(&credential.Location)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("credential %s: %w", name, err))
		case delivered:
			values[name] = value
		case credential.Required:
			errs = append(errs, fmt.Errorf("credential %s is required for %s but was not provided", name, action))
		}
	}
	return values, errors.Join(errs...)
}

// readRelocation reads the relocation mapping, when the runtime mounted one.
func (container Container) readRelocation() (relocation.ImageRelocationMap, error) {
	data, err := container.readFile(cnab.RelocationMapFile)
	if errors.Is(err, fs.ErrNotExist) {
		return relocation.ImageRelocationMap{}, nil
	}
	if err != nil {
		return nil, err
	}

	var mapping relocation.ImageRelocationMap
	if err := json.Unmarshal(data, &mapping); err != nil {
		return nil, fmt.Errorf("%s: %w", cnab.RelocationMapFile, err)
	}

	return mapping, nil
}

// deliver reads a value from where the bundle says the runtime puts it: the
// environment variable first, then the file, as cnab-go writes both.
func (container Container) deliver(location *bundle.Location) (string, bool, error) {
	if location == nil {
		return "", false, nil
	}

	if location.EnvironmentVariable != "" {
		if value, ok := container.LookupEnv(location.EnvironmentVariable); ok {
			return value, true, nil
		}
	}

	if location.Path == "" {
		return "", false, nil
	}

	data, err := container.readFile(location.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	return string(data), true, nil
}

// readFile reads an absolute path of the container.
func (container Container) readFile(path string) ([]byte, error) {
	data, err := fs.ReadFile(container.FS, strings.TrimPrefix(path, "/"))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return data, nil
}
