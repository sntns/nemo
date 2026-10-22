package compose

import (
	"cmp"
	"context"
	"slices"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/installer/compose/convert"
	"github.com/sntns/nemo/pkg/labels"
	"github.com/sntns/nemo/pkg/progress"
)

// Uninstall deletes what Install created from the project, as docker compose
// down does: the containers of its services, dependants first, then its
// networks, and its volumes when asked. What Install deleted as orphans is
// not looked for.
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

// objects are objects of an installation, found on Docker, to delete.
type objects struct {
	containers []container.Summary
	networks   []network.Summary
	volumes    []*volume.Volume
}

// delete deletes the containers first, so that nothing uses the networks and
// volumes any more.
func (orchestrator *Orchestrator) delete(ctx context.Context, found objects, events progress.Sink) error {
	if err := orchestrator.deleteContainers(ctx, found.containers, events); err != nil {
		return err
	}
	if err := orchestrator.deleteNetworks(ctx, found.networks, events); err != nil {
		return err
	}
	return orchestrator.deleteVolumes(ctx, found.volumes, events)
}

// declared returns the objects of the project's specs that are there, found
// by name, and the volumes too when asked. A network or a volume of that name
// that is not the stack's is kept, with a warning: an uninstall still cleans
// up after an install that refused it.
func (orchestrator *Orchestrator) declared(ctx context.Context, project *installer.Project, specs *projectSpecs, volumes bool, events progress.Sink) (objects, error) {
	var found objects
	want := stackLabels(project.Installation)

	for _, level := range slices.Backward(project.Levels) {
		for _, name := range level {
			containers, err := orchestrator.containers(ctx, specs.services[name])
			if err != nil {
				return found, err
			}
			found.containers = append(found.containers, containers...)
		}
	}

	for _, spec := range specs.networks {
		existing, err := orchestrator.docker.NetworkFind(ctx, spec.Name)
		switch {
		case err != nil:
			return found, err
		case existing == nil:
		case !docker.Owns(existing.Labels, want):
			events.Emit(progress.Event{Group: "networks", Item: spec.Name, State: progress.Warning, Message: "kept: " + docker.NotOwned("network", spec.Name, want).Error()})
		default:
			found.networks = append(found.networks, *existing)
		}
	}

	if !volumes {
		return found, nil
	}
	owner := installer.VolumeOwner(project.Installation)
	for _, spec := range specs.volumes {
		existing, err := orchestrator.docker.VolumeFind(ctx, spec.Name)
		switch {
		case err != nil:
			return found, err
		case existing == nil:
		case !docker.Owns(existing.Labels, owner):
			events.Emit(progress.Event{Group: "volumes", Item: spec.Name, State: progress.Warning, Message: "kept: " + docker.NotOwned("volume", spec.Name, owner).Error()})
		default:
			found.volumes = append(found.volumes, existing)
		}
	}
	return found, nil
}

// containers returns the containers of a service of the installation, its
// replicas numbered by their label, as docker compose finds them.
func (orchestrator *Orchestrator) containers(ctx context.Context, spec *convert.ServiceSpec) ([]container.Summary, error) {
	return orchestrator.docker.ContainerList(ctx, client.Filters{}.Add("label",
		labels.Stack+"="+spec.Config.Labels[labels.Stack],
		labels.Bundle+"="+spec.Config.Labels[labels.Bundle],
		labels.Service+"="+spec.Name,
	))
}

// deleteContainers deletes the containers, and says it once per service.
func (orchestrator *Orchestrator) deleteContainers(ctx context.Context, containers []container.Summary, events progress.Sink) error {
	var services []string
	for _, summary := range containers {
		if err := orchestrator.docker.ContainerDelete(ctx, summary.ID); err != nil {
			return err
		}
		if service := summary.Labels[labels.Service]; !slices.Contains(services, service) {
			services = append(services, service)
		}
	}
	for _, service := range services {
		events.Emit(progress.Event{Group: "services", Item: service, State: progress.Done, Message: "removed"})
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
