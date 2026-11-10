package cnab

import "encoding/json"

// ParameterSourcesExtension gives parameters a source: an action not given
// one reads it from an output of the installation's last runs, which
// carries a value from one action to the next. (cnab-spec
// schema/parameter-sources.schema.json)
var ParameterSourcesExtension = Extension[ParameterSources]{
	Key:       OfficialExtensionsPrefix + "parameter-sources",
	Shorthand: "parameter-sources",
	Schema:    "io.cnab.parameter-sources.schema.json",
}

// ParameterSources are the sources of the parameters, by name.
type ParameterSources map[string]ParameterSource

// ParameterSource is where a parameter is read from: its sources by type,
// in the order of priority.
type ParameterSource struct {
	Priority []string                   `json:"priority"`
	Sources  map[string]json.RawMessage `json:"sources"`
}

const (
	// SourceOutput is the type of a source that is an output of the installation.
	SourceOutput = "output"
	// SourceDependencyOutput is the type of a source that is an output of a
	// dependency, as Porter adds it: the spec defines only SourceOutput.
	SourceDependencyOutput = "dependencies.output"
)

// Output is the output the parameter is read from, when it has one.
func (source ParameterSource) Output() (string, bool) {
	for _, kind := range source.Priority {
		var output struct {
			Name string `json:"name"`
		}
		if kind == SourceOutput && json.Unmarshal(source.Sources[kind], &output) == nil && output.Name != "" {
			return output.Name, true
		}
	}
	return "", false
}

// DependencyOutput is the dependency, by its key in requires, and the output
// of it the parameter is read from, when it has one.
func (source ParameterSource) DependencyOutput() (dependency, output string, ok bool) {
	for _, kind := range source.Priority {
		var from struct {
			Dependency string `json:"dependency"`
			Name       string `json:"name"`
		}
		if kind == SourceDependencyOutput && json.Unmarshal(source.Sources[kind], &from) == nil && from.Dependency != "" && from.Name != "" {
			return from.Dependency, from.Name, true
		}
	}
	return "", "", false
}

// IsSourced reports whether the parameter is read from a source when an
// action is not given it.
func (bundle ExtendedBundle) IsSourced(parameter string) bool {
	if bundle.ParameterSources == nil {
		return false
	}
	_, sourced := (*bundle.ParameterSources)[parameter]
	return sourced
}
