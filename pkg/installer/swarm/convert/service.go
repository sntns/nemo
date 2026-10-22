package convert

import (
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/labels"
)

// Service converts a compose service into the spec of a Swarm service.
// configs and secrets map the compose keys to the names of the Swarm objects;
// base holds the labels of the installation.
//
// As with docker stack deploy, deploy.labels go on the service and labels
// on its containers.
func Service(namespace Namespace, project *types.Project, service types.ServiceConfig, configs, secrets, base map[string]string) (*swarm.ServiceSpec, error) {
	deploy := service.Deploy
	if deploy == nil {
		deploy = &types.DeployConfig{}
	}
	isolation := container.IsolationDefault
	if service.Isolation != "" {
		isolation = container.Isolation(service.Isolation)
	}
	own := map[string]string{labels.Service: service.Name}
	spec := &swarm.ServiceSpec{
		Annotations: swarm.Annotations{
			Name:   namespace.Scope(service.Name),
			Labels: labels.Merge(deploy.Labels, base, own),
		},
		TaskTemplate: swarm.TaskSpec{
			ContainerSpec: &swarm.ContainerSpec{
				Image:           service.Image,
				Command:         service.Entrypoint,
				Args:            service.Command,
				Hostname:        service.Hostname,
				Env:             docker.Environment(service.Environment),
				Labels:          labels.Merge(service.Labels, base, own),
				Dir:             service.WorkingDir,
				User:            service.User,
				Groups:          service.GroupAdd,
				StopGracePeriod: (*time.Duration)(service.StopGracePeriod),
				StopSignal:      service.StopSignal,
				TTY:             service.Tty,
				OpenStdin:       service.StdinOpen,
				ReadOnly:        service.ReadOnly,
				Isolation:       isolation,
				Init:            service.Init,
				Sysctls:         service.Sysctls,
				CapabilityAdd:   service.CapAdd,
				CapabilityDrop:  service.CapDrop,
				Hosts:           extraHosts(service.ExtraHosts),
				Ulimits:         docker.Ulimits(service.Ulimits),
				Healthcheck:     docker.Healthcheck(service.HealthCheck),
			},
			Resources:     resources(deploy.Resources),
			RestartPolicy: restartPolicy(deploy.RestartPolicy),
			LogDriver:     logDriver(service.Logging),
		},
		UpdateConfig:   updateConfig(deploy.UpdateConfig),
		RollbackConfig: updateConfig(deploy.RollbackConfig),
	}
	var err error
	if spec.Mode, err = serviceMode(service, deploy); err != nil {
		return nil, err
	}
	if spec.TaskTemplate.Placement, err = placement(service, deploy.Placement); err != nil {
		return nil, err
	}
	if spec.TaskTemplate.ContainerSpec.DNSConfig, err = dnsConfig(service); err != nil {
		return nil, err
	}
	if spec.TaskTemplate.ContainerSpec.Mounts, err = Mounts(service, project.Volumes, base); err != nil {
		return nil, err
	}
	if spec.TaskTemplate.Networks, err = networkAttachments(service, project.Networks); err != nil {
		return nil, err
	}
	if spec.TaskTemplate.ContainerSpec.Configs, err = configReferences(service, configs); err != nil {
		return nil, err
	}
	if spec.TaskTemplate.ContainerSpec.Secrets, err = secretReferences(service, secrets); err != nil {
		return nil, err
	}
	if spec.EndpointSpec, err = endpoint(service, deploy); err != nil {
		return nil, err
	}
	spec.Labels[labels.Hash], err = Hash(spec)
	return spec, err
}
