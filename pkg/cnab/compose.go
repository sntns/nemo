package cnab

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
)

// ComposeSchemaVersion is the version of the compose extension nemo writes.
const ComposeSchemaVersion = "1.0.0"

// ComposeExtension holds a bundle's compose project. The installer applies
// it with Compose or Swarm, whichever the target runs: the bundle says what
// runs, never where. It is an installer extension, so a bundle never
// requires it (see InstallerExtensions).
var ComposeExtension = Extension[Compose]{
	Key:    NemoExtensionsPrefix + "compose",
	Schema: "io.nemo.compose.schema.json",
}

// Compose is the content of the compose extension.
type Compose struct {
	SchemaVersion string `json:"schemaVersion"`

	// Documents are merged in order into one project, like several -f flags.
	Documents []ComposeDocument `json:"compose"`
}

// ComposeDocument is one compose document, as parsed YAML.
type ComposeDocument struct {
	// Name is the file name shown in errors.
	Name   string         `json:"name,omitempty"`
	Config map[string]any `json:"config"`
}

// Load merges the documents into the compose project name, its relative
// paths resolved in workingDir. The documents are interpolated with
// environment; with none, as when a bundle is built, they are not, and
// keep their variables. The documents are left as they are: the loader
// normalizes what it is given, so it is given a copy.
func (compose Compose) Load(ctx context.Context, name, workingDir string, environment types.Mapping) (*types.Project, error) {
	files := make([]types.ConfigFile, len(compose.Documents))
	for i, document := range compose.Documents {
		data, err := json.Marshal(document.Config)
		if err != nil {
			return nil, err
		}
		var config map[string]any
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, err
		}
		integers(config)
		files[i] = types.ConfigFile{Filename: cmp.Or(document.Name, fmt.Sprintf("compose-%d.yaml", i)), Config: config}
	}
	return loader.LoadWithContext(ctx, types.ConfigDetails{WorkingDir: workingDir, ConfigFiles: files, Environment: environment}, func(options *loader.Options) {
		options.SetProjectName(name, true)
		if environment == nil {
			options.SkipInterpolation = true
			options.SkipResolveEnvironment = true
		}
	})
}

// integers turns back into an int each number of a JSON document that has no
// fraction. JSON has one number type, which decodes as a float64, while the
// compose loader takes the int a YAML document gives it: for a published
// port, a file mode or an ulimit, it rejects a float64, or panics.
func integers(value any) any {
	switch value := value.(type) {
	case map[string]any:
		for key, element := range value {
			value[key] = integers(element)
		}
	case []any:
		for i, element := range value {
			value[i] = integers(element)
		}
	case float64:
		if value == math.Trunc(value) && math.Abs(value) <= 1<<53 {
			return int(value)
		}
	}
	return value
}
