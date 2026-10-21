package installer

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/template"

	"github.com/compose-spec/compose-go/v2/types"
	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/sntns/nemo/internal/decode"
	"github.com/sntns/nemo/pkg/labels"
	"github.com/sntns/nemo/pkg/target"
)

// VolumeExtension is the compose extension by which a bundle gives a volume
// a type, that the target makes of a driver and options.
const VolumeExtension = "x-nemo-volume"

// VolumeOwner are the labels that make a volume the stack's own. A network
// or a container is also its orchestrator's, a volume is not: the data an
// uninstall keeps is for the next install, whichever orchestrator it uses.
func VolumeOwner(installation Installation) map[string]string {
	return map[string]string{labels.Stack: installation.Stack}
}

// VolumeClaim is what a bundle says of a volume in its x-nemo-volume.
type VolumeClaim struct {
	Type    string   `json:"type"`
	Options []string `json:"options,omitempty"`
}

// resolveVolumes gives each volume with a type the driver and options the
// target's type makes it of, and returns what to provision before each
// one, by the volume's name. A volume without a type stays as written.
func resolveVolumes(compose *types.Project, stack string, volumeTypes map[string]target.VolumeType) (map[string]target.VolumeProvision, error) {
	provisions := map[string]target.VolumeProvision{}
	for _, key := range slices.Sorted(maps.Keys(compose.Volumes)) {
		volume := compose.Volumes[key]
		raw, declared := volume.Extensions[VolumeExtension]
		if !declared {
			continue
		}
		var claim VolumeClaim
		if err := decode.Strict(raw, &claim); err != nil {
			return nil, fmt.Errorf("volumes.%s.%s: %w", key, VolumeExtension, err)
		}
		names := slices.Sorted(maps.Keys(volumeTypes))
		typeOnly := validation.Empty.Error("give a type or a driver, not both: the target makes the type of a driver")
		if err := (validation.Errors{
			"external":    validation.Validate(bool(volume.External), validation.Empty.Error("an external volume can't have a type")),
			"driver":      validation.Validate(volume.Driver, typeOnly),
			"driver_opts": validation.Validate(volume.DriverOpts, typeOnly),
			VolumeExtension: validation.Validate(claim.Type, validation.Required, validation.By(func(any) error {
				if _, known := volumeTypes[claim.Type]; !known {
					return fmt.Errorf("the target has no volume type %s, it has %s", claim.Type, strings.Join(names, ", "))
				}
				return nil
			})),
		}).Filter(); err != nil {
			return nil, fmt.Errorf("volumes.%s: %w", key, err)
		}
		volumeType := volumeTypes[claim.Type]

		data := map[string]any{"Stack": stack, "Name": key, "Options": claim.Options}
		var err error
		if volume.Driver, err = render(volumeType.Driver, data); err != nil {
			return nil, fmt.Errorf("volume type %s: driver: %w", claim.Type, err)
		}
		if len(volumeType.Options) > 0 {
			volume.DriverOpts = map[string]string{}
		}
		for name, value := range volumeType.Options {
			if volume.DriverOpts[name], err = render(value, data); err != nil {
				return nil, fmt.Errorf("volume type %s: options.%s: %w", claim.Type, name, err)
			}
		}
		compose.Volumes[key] = volume

		if volumeType.Provision != nil {
			provision := *volumeType.Provision
			if provision.Directory, err = render(provision.Directory, data); err != nil {
				return nil, fmt.Errorf("volume type %s: provision.directory: %w", claim.Type, err)
			}
			provisions[volume.Name] = provision
		}
	}
	return provisions, nil
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
