package cnab

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// OverrideSources returns the variables an override reads the contents of
// its secrets and configs from: the environment of each.
func OverrideSources(override string) ([]string, error) {
	document, err := overrideDocument(override)
	if document == nil || err != nil {
		return nil, err
	}
	var sources []string
	for _, kind := range []string{"secrets", "configs"} {
		for _, object := range entries(value(document, kind)) {
			if source := value(object.value, "environment"); source != nil && source.Kind == yaml.ScalarNode && !slices.Contains(sources, source.Value) {
				sources = append(sources, source.Value)
			}
		}
	}
	slices.Sort(sources)
	return sources, nil
}

// CheckOverride refuses an override that changes what the bundle pins: the
// image of a service, built or pulled, or a service of its own, which would
// need one.
func (compose Compose) CheckOverride(override string) error {
	document, err := overrideDocument(override)
	if document == nil || err != nil {
		return err
	}
	var errs []error
	for _, service := range entries(value(document, "services")) {
		if !compose.hasService(service.key) {
			errs = append(errs, fmt.Errorf("override: services.%s is not a service of the bundle: an override changes the bundle's services, it adds none", service.key))
			continue
		}
		for _, key := range []string{"image", "build"} {
			if value(service.value, key) != nil {
				errs = append(errs, fmt.Errorf("override: services.%s.%s: the bundle pins its images, an override can't change them", service.key, key))
			}
		}
	}
	return errors.Join(errs...)
}

func (compose Compose) hasService(name string) bool {
	return slices.ContainsFunc(compose.Documents, func(document ComposeDocument) bool {
		services, _ := document.Config["services"].(map[string]any)
		_, ok := services[name]
		return ok
	})
}

// overrideDocument parses the override as nodes, which keep the !reset and
// !override tags the compose loader reads. An empty override is none.
func overrideDocument(override string) (*yaml.Node, error) {
	if strings.TrimSpace(override) == "" {
		return nil, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(override), &root); err != nil {
		return nil, fmt.Errorf("override: %w", err)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("override: a compose document, a mapping of services, secrets, configs…")
	}
	return root.Content[0], nil
}

type entry struct {
	key   string
	value *yaml.Node
}

func entries(mapping *yaml.Node) []entry {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	var result []entry
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		result = append(result, entry{key: mapping.Content[i].Value, value: mapping.Content[i+1]})
	}
	return result
}

func value(mapping *yaml.Node, key string) *yaml.Node {
	for _, entry := range entries(mapping) {
		if entry.key == key {
			return entry.value
		}
	}
	return nil
}
