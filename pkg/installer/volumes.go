package installer

import (
	"fmt"
	"maps"
	"slices"
	"strings"

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
		rendered, err := volumeTypes[claim.Type].Render(stack, key, claim.Options)
		if err != nil {
			return nil, fmt.Errorf("volume type %s: %w", claim.Type, err)
		}
		volume.Driver, volume.DriverOpts = rendered.Driver, rendered.Options
		compose.Volumes[key] = volume
		if rendered.Provision != nil {
			provisions[volume.Name] = *rendered.Provision
		}
	}
	return provisions, nil
}
