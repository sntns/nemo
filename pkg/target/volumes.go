package target

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// Render returns the type made for one volume: its driver, its options and
// its provisioned directory executed as templates of the volume's stack, its
// name and the options its x-nemo-volume gives.
func (volumeType VolumeType) Render(stack, name string, options []string) (VolumeType, error) {
	data := map[string]any{"Stack": stack, "Name": name, "Options": options}
	var rendered VolumeType
	var err error
	if rendered.Driver, err = render(volumeType.Driver, data); err != nil {
		return VolumeType{}, fmt.Errorf("driver: %w", err)
	}
	if len(volumeType.Options) > 0 {
		rendered.Options = map[string]string{}
	}
	for key, value := range volumeType.Options {
		if rendered.Options[key], err = render(value, data); err != nil {
			return VolumeType{}, fmt.Errorf("options.%s: %w", key, err)
		}
	}
	if volumeType.Provision != nil {
		provision := *volumeType.Provision
		if provision.Directory, err = render(provision.Directory, data); err != nil {
			return VolumeType{}, fmt.Errorf("provision.directory: %w", err)
		}
		rendered.Provision = &provision
	}
	return rendered, nil
}

// render executes a template of a volume type; a field it does not know is an error.
func render(text string, data map[string]any) (string, error) {
	parsed, err := template.New("").Option("missingkey=error").Funcs(template.FuncMap{"AppendJoin": appendJoin}).Parse(text)
	if err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	if err = parsed.Execute(&buffer, data); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

// appendJoin joins the elements, each after the separator: ready to append
// to a list that already has one.
func appendJoin(elements []string, separator string) string {
	if len(elements) == 0 {
		return ""
	}
	return separator + strings.Join(elements, separator)
}
