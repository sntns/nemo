package cnab

import (
	"fmt"

	"github.com/compose-spec/compose-go/v2/template"
)

// OutputsDirectory is where the invocation image writes its outputs
// (cnab-spec 103-bundle-runtime.md).
const OutputsDirectory = "/cnab/app/outputs"

// OutputsExtension holds the value of each output of a nemo bundle: a
// string its ${VARIABLE}s are the variables of the bundle's parameters in,
// as in its compose. The installer writes it where the output says once
// it installs or upgrades the bundle; the runtime works it out from the
// parameters of the installation, to give it to the installations that
// read it before the installer has run.
var OutputsExtension = Extension[OutputValues]{
	Key:    NemoExtensionsPrefix + "outputs",
	Schema: "io.nemo.outputs.schema.json",
}

// OutputValues are the values of the outputs, by name.
type OutputValues map[string]OutputValue

// OutputValue is the value of an output.
type OutputValue struct {
	Value string `json:"value"`
}

// OutputValue returns the value of the output, interpolated with the
// variables lookup finds, as the compose is.
func (bundle ExtendedBundle) OutputValue(name string, lookup func(variable string) (string, bool)) (string, error) {
	if bundle.OutputValues == nil {
		return "", fmt.Errorf("output %s: the bundle has no value for its outputs", name)
	}
	output, ok := (*bundle.OutputValues)[name]
	if !ok {
		return "", fmt.Errorf("output %s: the bundle has no such output", name)
	}
	value, err := template.Substitute(output.Value, lookup)
	if err != nil {
		return "", fmt.Errorf("output %s: %w", name, err)
	}
	return value, nil
}

// OutputLookup finds the variables of the bundle's parameters in their
// values, by name, as the installer reads them: a parameter not given has
// its default, else reads as blank.
func (bundle ExtendedBundle) OutputLookup(parameters map[string]string) func(variable string) (string, bool) {
	variables := map[string]string{}
	for name, parameter := range bundle.Parameters {
		if parameter.Destination == nil || parameter.Destination.EnvironmentVariable == "" {
			continue
		}
		value, given := parameters[name]
		if definition, ok := bundle.Definitions[parameter.Definition]; !given && ok && definition.Default != nil {
			value, _ = WriteParameterToString(name, definition.Default)
		}
		variables[parameter.Destination.EnvironmentVariable] = value
	}
	return func(variable string) (string, bool) {
		value, ok := variables[variable]
		return value, ok
	}
}
