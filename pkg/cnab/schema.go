package cnab

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaFiles holds one schema per extension, named after its key. See
// schemas/README.md for where each one comes from.
//
//go:embed schemas/*.json
var schemaFiles embed.FS

// schemas compiles every embedded schema once, keyed by its file name.
var schemas = sync.OnceValues(func() (map[string]*jsonschema.Schema, error) {
	entries, err := fs.ReadDir(schemaFiles, "schemas")
	if err != nil {
		return nil, err
	}

	compiler := jsonschema.NewCompiler()
	for _, entry := range entries {
		data, err := schemaFiles.ReadFile("schemas/" + entry.Name())
		if err != nil {
			return nil, err
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("schemas/%s: %w", entry.Name(), err)
		}
		if err := compiler.AddResource(schemaLocation(entry.Name()), document); err != nil {
			return nil, fmt.Errorf("schemas/%s: %w", entry.Name(), err)
		}
	}

	compiled := make(map[string]*jsonschema.Schema, len(entries))
	for _, entry := range entries {
		if compiled[entry.Name()], err = compiler.Compile(schemaLocation(entry.Name())); err != nil {
			return nil, fmt.Errorf("compile schemas/%s: %w", entry.Name(), err)
		}
	}
	return compiled, nil
})

// schemaLocation is where the compiler finds an embedded schema.
func schemaLocation(file string) string {
	return "embed:///schemas/" + file
}

// validateSchema checks a JSON document against the embedded schema in file.
func validateSchema(file string, raw []byte) error {
	all, err := schemas()
	if err != nil {
		return fmt.Errorf("load embedded schemas: %w", err)
	}
	schema, ok := all[file]
	if !ok {
		return fmt.Errorf("no embedded schema %s", file)
	}

	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if err := schema.Validate(document); err != nil {
		return err
	}
	return nil
}
