package convert

import (
	"fmt"
	"os"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/mount"
)

func mounts(service types.ServiceConfig, volumes types.Volumes) ([]mount.Mount, error) {
	var result []mount.Mount
	for _, volume := range service.Volumes {
		serviceMount := mount.Mount{Type: mount.Type(volume.Type), Source: volume.Source, Target: volume.Target, ReadOnly: volume.ReadOnly, Consistency: mount.Consistency(volume.Consistency)}
		switch volume.Type {
		case types.VolumeTypeVolume, "":
			serviceMount.Type = mount.TypeVolume
			if volume.Source != "" {
				declared, ok := volumes[volume.Source]
				if !ok {
					return nil, fmt.Errorf("volume %s: undefined volume %q", volume.Target, volume.Source)
				}
				serviceMount.Source = declared.Name
			}
			if volume.Volume != nil {
				serviceMount.VolumeOptions = &mount.VolumeOptions{NoCopy: volume.Volume.NoCopy, Subpath: volume.Volume.Subpath}
			}
		case types.VolumeTypeBind:
			if volume.Bind != nil {
				serviceMount.BindOptions = &mount.BindOptions{Propagation: mount.Propagation(volume.Bind.Propagation), CreateMountpoint: bool(volume.Bind.CreateHostPath)}
			}
		case types.VolumeTypeTmpfs:
			if volume.Tmpfs != nil {
				serviceMount.TmpfsOptions = &mount.TmpfsOptions{SizeBytes: int64(volume.Tmpfs.Size), Mode: os.FileMode(volume.Tmpfs.Mode)}
			}
		case types.VolumeTypeNamedPipe:
		default:
			return nil, fmt.Errorf("volume %s: type %q is not supported by Compose", volume.Target, volume.Type)
		}
		result = append(result, serviceMount)
	}
	return result, nil
}
