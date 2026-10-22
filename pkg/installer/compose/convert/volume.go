package convert

import (
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/volume"

	"github.com/sntns/nemo/pkg/labels"
)

// Volume converts a compose volume.
func Volume(key string, config types.VolumeConfig, base map[string]string) *volume.Volume {
	return &volume.Volume{Name: config.Name, Driver: config.Driver, Options: config.DriverOpts, Labels: labels.Merge(config.Labels, base, map[string]string{VolumeLabel: key})}
}
