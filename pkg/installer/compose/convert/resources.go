package convert

import (
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/container"

	"github.com/sntns/nemo/pkg/docker"
)

// resources takes the top-level attributes, or deploy.resources when they are not set, as docker
// compose does.
func resources(service types.ServiceConfig) container.Resources {
	resources := container.Resources{
		NanoCPUs:          docker.NanoCPUs(service.CPUS),
		Memory:            int64(service.MemLimit),
		MemoryReservation: int64(service.MemReservation),
		MemorySwap:        int64(service.MemSwapLimit),
		CPUShares:         service.CPUShares,
		CPUPeriod:         service.CPUPeriod,
		CPUQuota:          service.CPUQuota,
		CpusetCpus:        service.CPUSet,
		CgroupParent:      service.CgroupParent,
		DeviceCgroupRules: service.DeviceCgroupRules,
		Ulimits:           docker.Ulimits(service.Ulimits),
	}
	if service.MemSwappiness != 0 {
		swappiness := int64(service.MemSwappiness)
		resources.MemorySwappiness = &swappiness
	}
	if service.OomKillDisable {
		resources.OomKillDisable = &service.OomKillDisable
	}
	if service.PidsLimit != 0 {
		resources.PidsLimit = &service.PidsLimit
	}
	for _, device := range service.Devices {
		resources.Devices = append(resources.Devices, container.DeviceMapping{PathOnHost: device.Source, PathInContainer: device.Target, CgroupPermissions: device.Permissions})
	}
	if service.Deploy == nil {
		return resources
	}
	if limits := service.Deploy.Resources.Limits; limits != nil {
		if resources.NanoCPUs == 0 {
			resources.NanoCPUs = docker.NanoCPUs(limits.NanoCPUs.Value())
		}
		if resources.Memory == 0 {
			resources.Memory = int64(limits.MemoryBytes)
		}
		if resources.PidsLimit == nil && limits.Pids != 0 {
			resources.PidsLimit = &limits.Pids
		}
	}
	if reservations := service.Deploy.Resources.Reservations; reservations != nil && resources.MemoryReservation == 0 {
		resources.MemoryReservation = int64(reservations.MemoryBytes)
	}
	return resources
}
