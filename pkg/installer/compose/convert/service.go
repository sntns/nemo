package convert

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/labels"
)

// ServiceSpec is what every container of a compose service is created from.
type ServiceSpec struct {
	Name       string
	Replicas   int
	Config     *container.Config
	Host       *container.HostConfig
	Networking *network.NetworkingConfig
	Platform   *ocispec.Platform
	Files      []File
	// Hash identifies the spec: a container whose hash differs is recreated.
	Hash string
	// Wait is the state the containers must reach before the next level starts.
	Wait Condition
}

// Service converts a compose service into what each of its containers is
// created from.
func Service(project *types.Project, service types.ServiceConfig, base map[string]string) (*ServiceSpec, error) {
	spec := &ServiceSpec{Name: service.Name, Replicas: service.GetScale()}
	own := map[string]string{labels.Service: service.Name, ServiceLabel: service.Name, OneoffLabel: "False"}
	spec.Config = &container.Config{
		Image:        service.Image,
		Entrypoint:   service.Entrypoint,
		Cmd:          service.Command,
		Env:          docker.Environment(service.Environment),
		Labels:       labels.Merge(service.Labels, base, own),
		Hostname:     service.Hostname,
		Domainname:   service.DomainName,
		User:         service.User,
		WorkingDir:   service.WorkingDir,
		Tty:          service.Tty,
		OpenStdin:    service.StdinOpen,
		StopSignal:   service.StopSignal,
		Healthcheck:  docker.Healthcheck(service.HealthCheck),
		ExposedPorts: network.PortSet{},
	}
	if service.StopGracePeriod != nil {
		seconds := int(time.Duration(*service.StopGracePeriod).Seconds())
		spec.Config.StopTimeout = &seconds
	}
	spec.Host = &container.HostConfig{
		CapAdd:         service.CapAdd,
		CapDrop:        service.CapDrop,
		DNSOptions:     service.DNSOpts,
		DNSSearch:      service.DNSSearch,
		ExtraHosts:     extraHosts(service.ExtraHosts),
		GroupAdd:       service.GroupAdd,
		IpcMode:        container.IpcMode(service.Ipc),
		PidMode:        container.PidMode(service.Pid),
		UTSMode:        container.UTSMode(service.Uts),
		UsernsMode:     container.UsernsMode(service.UserNSMode),
		CgroupnsMode:   container.CgroupnsMode(service.Cgroup),
		Privileged:     service.Privileged,
		ReadonlyRootfs: service.ReadOnly,
		SecurityOpt:    service.SecurityOpt,
		ShmSize:        int64(service.ShmSize),
		Sysctls:        service.Sysctls,
		Runtime:        service.Runtime,
		StorageOpt:     service.StorageOpt,
		Annotations:    service.Annotations,
		Isolation:      container.Isolation(service.Isolation),
		Init:           service.Init,
		OomScoreAdj:    int(service.OomScoreAdj),
		VolumeDriver:   service.VolumeDriver,
		Tmpfs:          tmpfs(service.Tmpfs),
		RestartPolicy:  restartPolicy(service),
		Resources:      resources(service),
	}
	if service.Logging != nil {
		spec.Host.LogConfig = container.LogConfig{Type: service.Logging.Driver, Config: service.Logging.Options}
	}
	for _, from := range service.VolumesFrom {
		if name, ok := strings.CutPrefix(from, "service:"); ok {
			from = ContainerName(project.Name, name, 1)
		} else {
			from = strings.TrimPrefix(from, "container:")
		}
		spec.Host.VolumesFrom = append(spec.Host.VolumesFrom, from)
	}
	for _, server := range service.DNS {
		address, err := netip.ParseAddr(server)
		if err != nil {
			return nil, fmt.Errorf("dns: %w", err)
		}
		spec.Host.DNS = append(spec.Host.DNS, address)
	}
	var err error
	if err = ports(spec, service); err != nil {
		return nil, err
	}
	if spec.Host.Mounts, err = mounts(service, project.Volumes); err != nil {
		return nil, err
	}
	if err = networking(spec, project, service); err != nil {
		return nil, err
	}
	if spec.Files, err = files(project, service); err != nil {
		return nil, err
	}
	if service.Platform != "" {
		parts := strings.SplitN(service.Platform, "/", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("platform %q: expected os/architecture", service.Platform)
		}
		spec.Platform = &ocispec.Platform{OS: parts[0], Architecture: parts[1]}
		if len(parts) == 3 {
			spec.Platform.Variant = parts[2]
		}
	}
	data, err := json.Marshal(struct {
		Config     *container.Config
		Host       *container.HostConfig
		Networking *network.NetworkingConfig
		Platform   *ocispec.Platform
		Files      []File
	}{spec.Config, spec.Host, spec.Networking, spec.Platform, spec.Files})
	spec.Hash = digest.FromBytes(data).Encoded()
	return spec, err
}
