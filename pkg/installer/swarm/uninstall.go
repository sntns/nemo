package swarm

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/labels"
	"github.com/sntns/nemo/pkg/progress"
)

// serviceIDLabel is the label Swarm puts on the containers of a service's tasks.
const serviceIDLabel = "com.docker.swarm.service.id"

// Uninstall deletes what Install created from the project, as docker stack
// rm does: its services, then its configs and secrets, its networks, and its
// volumes when asked. What Install deleted as orphans is not looked for.
func (orchestrator *Orchestrator) Uninstall(ctx context.Context, project *installer.Project, opts installer.UninstallOptions) error {
	events := cmp.Or(opts.Events, progress.Discard)
	specs, err := build(project)
	if err != nil {
		return err
	}

	declared, err := orchestrator.declared(ctx, project, specs, opts.RemoveVolumes, events)
	if err != nil {
		return err
	}
	return orchestrator.delete(ctx, declared, events)
}

// objects are objects of an installation, found on the swarm, to delete.
type objects struct {
	services []swarm.Service
	configs  []swarm.Config
	secrets  []swarm.Secret
	networks []network.Summary
	volumes  []*volume.Volume
}

// delete deletes the services first, and waits until their containers are
// gone, so that nothing uses the rest any more.
func (orchestrator *Orchestrator) delete(ctx context.Context, found objects, events progress.Sink) error {
	if err := orchestrator.deleteServices(ctx, found.services, events); err != nil {
		return err
	}
	if err := orchestrator.deleteConfigs(ctx, found.configs, events); err != nil {
		return err
	}
	if err := orchestrator.deleteSecrets(ctx, found.secrets, events); err != nil {
		return err
	}
	if err := orchestrator.deleteNetworks(ctx, found.networks, events); err != nil {
		return err
	}
	return orchestrator.deleteVolumes(ctx, found.volumes, events)
}

// declared returns the objects of the project's specs that are there, found
// by name, and the volumes too when asked. One of that name that is not the
// installation's, or the stack's for a network or a volume, is kept, with a
// warning: an uninstall still cleans up after an install that refused it.
// Configs and secrets are named after their content, which an uninstall does
// not read: every content of a declared one is found, by the prefix of its
// name.
func (orchestrator *Orchestrator) declared(ctx context.Context, project *installer.Project, specs *projectSpecs, volumes bool, events progress.Sink) (objects, error) {
	var found objects
	var err error
	if found.services, err = orchestrator.declaredServices(ctx, project, specs, events); err != nil {
		return found, err
	}
	if found.configs, err = orchestrator.declaredConfigs(ctx, project); err != nil {
		return found, err
	}
	if found.secrets, err = orchestrator.declaredSecrets(ctx, project); err != nil {
		return found, err
	}
	if found.networks, err = orchestrator.declaredNetworks(ctx, project, specs, events); err != nil {
		return found, err
	}
	if volumes {
		found.volumes, err = orchestrator.declaredVolumes(ctx, project, specs, events)
	}
	return found, err
}

// ours tells whether an object found by its name has the labels wanted: one
// that does not is kept, with a warning.
func ours(events progress.Sink, group, kind, name string, have, want map[string]string) bool {
	if docker.Owns(have, want) {
		return true
	}
	events.Emit(progress.Event{Group: group, Item: name, State: progress.Warning, Message: "kept: " + docker.NotOwned(kind, name, want).Error()})
	return false
}

func (orchestrator *Orchestrator) declaredServices(ctx context.Context, project *installer.Project, specs *projectSpecs, events progress.Sink) ([]swarm.Service, error) {
	want := installationLabels(project.Installation)
	var found []swarm.Service
	for _, name := range slices.Sorted(maps.Keys(specs.services)) {
		spec := specs.services[name]
		existing, err := orchestrator.docker.ServiceFind(ctx, spec.Name)
		if err != nil {
			return nil, err
		}
		if existing != nil && ours(events, "services", "service", spec.Name, existing.Spec.Labels, want) {
			found = append(found, *existing)
		}
	}
	return found, nil
}

func (orchestrator *Orchestrator) declaredConfigs(ctx context.Context, project *installer.Project) ([]swarm.Config, error) {
	want := installationLabels(project.Installation)
	var found []swarm.Config
	for _, key := range slices.Sorted(maps.Keys(project.Compose.Configs)) {
		config := project.Compose.Configs[key]
		if config.External {
			continue
		}
		configs, err := orchestrator.docker.ConfigList(ctx, docker.ByName(config.Name+"-"))
		if err != nil {
			return nil, err
		}
		for _, existing := range configs {
			if docker.Owns(existing.Spec.Labels, labels.Merge(want, map[string]string{labels.Config: key})) {
				found = append(found, existing)
			}
		}
	}
	return found, nil
}

func (orchestrator *Orchestrator) declaredSecrets(ctx context.Context, project *installer.Project) ([]swarm.Secret, error) {
	want := installationLabels(project.Installation)
	var found []swarm.Secret
	for _, key := range slices.Sorted(maps.Keys(project.Compose.Secrets)) {
		secret := project.Compose.Secrets[key]
		if secret.External {
			continue
		}
		secrets, err := orchestrator.docker.SecretList(ctx, docker.ByName(secret.Name+"-"))
		if err != nil {
			return nil, err
		}
		for _, existing := range secrets {
			if docker.Owns(existing.Spec.Labels, labels.Merge(want, map[string]string{labels.Secret: key})) {
				found = append(found, existing)
			}
		}
	}
	return found, nil
}

func (orchestrator *Orchestrator) declaredNetworks(ctx context.Context, project *installer.Project, specs *projectSpecs, events progress.Sink) ([]network.Summary, error) {
	want := stackLabels(project.Installation)
	var found []network.Summary
	for _, spec := range specs.networks {
		existing, err := orchestrator.docker.NetworkFind(ctx, spec.Name)
		if err != nil {
			return nil, err
		}
		if existing != nil && ours(events, "networks", "network", spec.Name, existing.Labels, want) {
			found = append(found, *existing)
		}
	}
	return found, nil
}

func (orchestrator *Orchestrator) declaredVolumes(ctx context.Context, project *installer.Project, specs *projectSpecs, events progress.Sink) ([]*volume.Volume, error) {
	owner := installer.VolumeOwner(project.Installation)
	var found []*volume.Volume
	for _, spec := range specs.volumes {
		existing, err := orchestrator.docker.VolumeFind(ctx, spec.Name)
		if err != nil {
			return nil, err
		}
		if existing != nil && ours(events, "volumes", "volume", spec.Name, existing.Labels, owner) {
			found = append(found, existing)
		}
	}
	return found, nil
}

// deleteServices deletes the services, and waits until their containers
// are gone: each service is stopping until then, and removed once none of
// its containers is left.
func (orchestrator *Orchestrator) deleteServices(ctx context.Context, services []swarm.Service, events progress.Sink) error {
	stopping := map[string]string{}
	for _, service := range services {
		if err := orchestrator.docker.ServiceDelete(ctx, &service); err != nil {
			return err
		}
		stopping[service.ID] = service.Spec.Labels[labels.Service]
		events.Emit(progress.Event{Group: "services", Item: service.Spec.Labels[labels.Service], State: progress.Working, Message: "stopping"})
	}
	return orchestrator.waitServicesGone(ctx, stopping, events)
}

// waitServicesGone waits until no container of the services stopping, by
// ID, is left. Swarm removes the containers of the tasks after the service,
// and they keep networks and volumes in use until then. It never removes
// those of tasks lost in a daemon crash: leftContainers removes them.
func (orchestrator *Orchestrator) waitServicesGone(ctx context.Context, stopping map[string]string, events progress.Sink) error {
	for len(stopping) > 0 {
		left, err := orchestrator.leftContainers(ctx, stopping)
		if err != nil {
			return err
		}
		for id, name := range stopping {
			if _, still := left[id]; !still {
				events.Emit(progress.Event{Group: "services", Item: name, State: progress.Done, Message: "removed"})
				delete(stopping, id)
			}
		}
		if len(left) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("containers still there: %s: %w", strings.Join(slices.Concat(slices.Collect(maps.Values(left))...), ", "), ctx.Err())
		case <-time.After(time.Second):
		}
	}
	return nil
}

// leftContainers returns the short IDs of the containers left of each
// service stopping, by service ID, and removes those that stopped. A dead
// container is not counted: the daemon cannot remove it.
func (orchestrator *Orchestrator) leftContainers(ctx context.Context, stopping map[string]string) (map[string][]string, error) {
	containers, err := orchestrator.docker.ContainerList(ctx, client.Filters{}.Add("label", serviceIDLabel))
	if err != nil {
		return nil, err
	}
	left := map[string][]string{}
	for _, summary := range containers {
		id := summary.Labels[serviceIDLabel]
		if _, waited := stopping[id]; !waited || summary.State == container.StateDead {
			continue
		}
		if summary.State == container.StateExited || summary.State == container.StateCreated {
			_ = orchestrator.docker.ContainerDelete(ctx, summary.ID)
		}
		left[id] = append(left[id], summary.ID[:12])
	}
	return left, nil
}

// deleteConfigs deletes the configs. One a service still uses is kept, with
// a warning.
func (orchestrator *Orchestrator) deleteConfigs(ctx context.Context, configs []swarm.Config, events progress.Sink) error {
	for _, existing := range configs {
		switch err := orchestrator.docker.ConfigDelete(ctx, &existing); {
		case err == nil:
			events.Emit(progress.Event{Group: "configs", Item: existing.Spec.Name, State: progress.Done, Message: "removed"})
		case docker.IsInUse(err):
			events.Emit(progress.Event{Group: "configs", Item: existing.Spec.Name, State: progress.Warning, Message: "kept: still in use"})
		default:
			return err
		}
	}
	return nil
}

// deleteSecrets deletes the secrets. One a service still uses is kept, with
// a warning.
func (orchestrator *Orchestrator) deleteSecrets(ctx context.Context, secrets []swarm.Secret, events progress.Sink) error {
	for _, existing := range secrets {
		switch err := orchestrator.docker.SecretDelete(ctx, &existing); {
		case err == nil:
			events.Emit(progress.Event{Group: "secrets", Item: existing.Spec.Name, State: progress.Done, Message: "removed"})
		case docker.IsInUse(err):
			events.Emit(progress.Event{Group: "secrets", Item: existing.Spec.Name, State: progress.Warning, Message: "kept: still in use"})
		default:
			return err
		}
	}
	return nil
}

// deleteNetworks deletes the networks. One another bundle of the stack still
// uses is kept: the stack's last bundle removes it.
func (orchestrator *Orchestrator) deleteNetworks(ctx context.Context, networks []network.Summary, events progress.Sink) error {
	for _, existing := range networks {
		switch err := orchestrator.docker.NetworkDelete(ctx, &existing); {
		case err == nil:
			events.Emit(progress.Event{Group: "networks", Item: existing.Name, State: progress.Done, Message: "removed"})
		case docker.IsInUse(err):
			events.Emit(progress.Event{Group: "networks", Item: existing.Name, State: progress.Done, Message: "kept: still in use"})
		default:
			return err
		}
	}
	return nil
}

// deleteVolumes deletes the volumes and their data. One a container still
// mounts is kept, with a warning.
func (orchestrator *Orchestrator) deleteVolumes(ctx context.Context, volumes []*volume.Volume, events progress.Sink) error {
	for _, existing := range volumes {
		switch err := orchestrator.docker.VolumeDelete(ctx, existing); {
		case err == nil:
			events.Emit(progress.Event{Group: "volumes", Item: existing.Name, State: progress.Done, Message: "removed"})
		case docker.IsInUse(err):
			events.Emit(progress.Event{Group: "volumes", Item: existing.Name, State: progress.Warning, Message: "kept: still in use"})
		default:
			return err
		}
	}
	return nil
}
