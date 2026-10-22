package convert

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/mount"

	"github.com/sntns/nemo/pkg/labels"
)

// Mounts converts the volumes and tmpfs of a service. A volume of the project
// carries its labels and driver, so that Swarm creates it on every node.
func Mounts(service types.ServiceConfig, volumes types.Volumes, base map[string]string) ([]mount.Mount, error) {
	var result []mount.Mount
	for _, volume := range service.Volumes {
		mount, err := serviceMount(volume, volumes, base)
		if err != nil {
			return nil, fmt.Errorf("volume %s: %w", volume.Target, err)
		}
		result = append(result, mount)
	}
	for _, target := range service.Tmpfs {
		result = append(result, mount.Mount{Type: mount.TypeTmpfs, Target: strings.TrimSpace(target)})
	}
	return result, nil
}

func serviceMount(volume types.ServiceVolumeConfig, volumes types.Volumes, base map[string]string) (mount.Mount, error) {
	result := mount.Mount{
		Type:        mount.Type(volume.Type),
		Target:      volume.Target,
		ReadOnly:    volume.ReadOnly,
		Source:      volume.Source,
		Consistency: mount.Consistency(volume.Consistency),
	}
	switch volume.Type {
	case types.VolumeTypeVolume, "":
		result.Type = mount.TypeVolume
		if volume.Tmpfs != nil || volume.Bind != nil {
			return mount.Mount{}, errors.New("tmpfs and bind options are incompatible with type volume")
		}
		if volume.Source == "" {
			return result, nil
		}
		declared, ok := volumes[volume.Source]
		if !ok {
			return mount.Mount{}, fmt.Errorf("undefined volume %q", volume.Source)
		}
		result.Source = declared.Name
		result.VolumeOptions = &mount.VolumeOptions{}
		if volume.Volume != nil {
			result.VolumeOptions.NoCopy = volume.Volume.NoCopy
			result.VolumeOptions.Subpath = volume.Volume.Subpath
		}
		if bool(declared.External) {
			return result, nil
		}
		// Swarm creates the volume on every node that runs a task, with these options.
		result.VolumeOptions.Labels = labels.Merge(declared.Labels, base)
		if declared.Driver != "" || declared.DriverOpts != nil {
			result.VolumeOptions.DriverConfig = &mount.Driver{Name: declared.Driver, Options: declared.DriverOpts}
		}
	case types.VolumeTypeBind:
		if volume.Source == "" {
			return mount.Mount{}, errors.New("a bind mount needs a source")
		}
		if volume.Volume != nil || volume.Tmpfs != nil {
			return mount.Mount{}, errors.New("volume and tmpfs options are incompatible with type bind")
		}
		if volume.Bind != nil {
			result.BindOptions = &mount.BindOptions{Propagation: mount.Propagation(volume.Bind.Propagation)}
		}
	case types.VolumeTypeTmpfs:
		if volume.Source != "" {
			return mount.Mount{}, errors.New("a tmpfs mount has no source")
		}
		if volume.Volume != nil || volume.Bind != nil {
			return mount.Mount{}, errors.New("volume and bind options are incompatible with type tmpfs")
		}
		if volume.Tmpfs != nil {
			result.TmpfsOptions = &mount.TmpfsOptions{SizeBytes: int64(volume.Tmpfs.Size), Mode: os.FileMode(volume.Tmpfs.Mode)}
		}
	case types.VolumeTypeNamedPipe:
		if volume.Source == "" {
			return mount.Mount{}, errors.New("a named pipe mount needs a source")
		}
	default:
		return mount.Mount{}, fmt.Errorf("type %q is not supported by swarm", volume.Type)
	}
	return result, nil
}
