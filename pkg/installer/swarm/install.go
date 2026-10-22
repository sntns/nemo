package swarm

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/volume"
	"golang.org/x/sync/errgroup"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/labels"
	"github.com/sntns/nemo/pkg/progress"
)

// Install creates what the project declares and is not there yet, the way
// docker stack deploy does: volumes, networks, configs and secrets, then the
// services, level after level, each waited for until it converged. It then
// deletes the services, configs and secrets the project no longer declares.
func (orchestrator *Orchestrator) Install(ctx context.Context, project *installer.Project, opts installer.InstallOptions) error {
	events := cmp.Or(opts.Events, progress.Discard)
	validate(project.Compose).EmitWarnings(events)
	specs, err := build(project)
	if err != nil {
		return err
	}

	if err = orchestrator.checkOwnership(ctx, project, specs); err != nil {
		return err
	}
	if err = orchestrator.createVolumes(ctx, project, specs.volumes, events); err != nil {
		return err
	}
	if err = orchestrator.createNetworks(ctx, specs.networks, events); err != nil {
		return err
	}
	if err = orchestrator.createConfigs(ctx, project, specs.configs, events); err != nil {
		return err
	}
	if err = orchestrator.createSecrets(ctx, project, specs.secrets, events); err != nil {
		return err
	}
	if err = orchestrator.createServices(ctx, project, specs, events); err != nil {
		return err
	}

	// Once everything converged, nothing uses what the project no longer declares.
	orphans, err := orchestrator.orphans(ctx, project, specs)
	if err != nil {
		return err
	}
	return orchestrator.delete(ctx, orphans, events)
}

// checkOwnership makes sure that the networks, volumes and services of the
// project that already exist are the stack's, as Swarm created them, before
// anything is created.
func (orchestrator *Orchestrator) checkOwnership(ctx context.Context, project *installer.Project, specs *projectSpecs) error {
	want := stackLabels(project.Installation)
	for _, spec := range specs.networks {
		existing, err := orchestrator.docker.NetworkFind(ctx, spec.Name)
		if err != nil {
			return err
		}
		if existing != nil && !docker.Owns(existing.Labels, want) {
			return docker.NotOwned("network", spec.Name, want)
		}
	}
	owner := installer.VolumeOwner(project.Installation)
	for _, spec := range specs.volumes {
		existing, err := orchestrator.docker.VolumeFind(ctx, spec.Name)
		if err != nil {
			return err
		}
		if existing != nil && !docker.Owns(existing.Labels, owner) {
			return docker.NotOwned("volume", spec.Name, owner)
		}
	}

	want = installationLabels(project.Installation)
	for _, name := range slices.Sorted(maps.Keys(specs.services)) {
		spec := specs.services[name]
		existing, err := orchestrator.docker.ServiceFind(ctx, spec.Name)
		if err != nil {
			return err
		}
		if existing != nil && !docker.Owns(existing.Spec.Labels, want) {
			return docker.NotOwned("service", spec.Name, want)
		}
	}
	return nil
}

// createVolumes creates the volumes that do not exist, after the directory
// their volume type provisions. A volume holds data: one that exists is
// never recreated to change its options.
func (orchestrator *Orchestrator) createVolumes(ctx context.Context, project *installer.Project, volumes []*volume.Volume, events progress.Sink) error {
	for _, spec := range volumes {
		existing, err := orchestrator.docker.VolumeFind(ctx, spec.Name)
		switch {
		case err != nil:
			return err
		case existing == nil:
			if provision, ok := project.Provisions[spec.Name]; ok {
				events.Emit(progress.Event{Group: "volumes", Item: spec.Name, State: progress.Working, Message: "provisioning " + provision.Directory})
				registryAuth, err := project.RegistryAuth.For(project.InvocationImage)
				if err != nil {
					return fmt.Errorf("volume %s: %w", spec.Name, err)
				}
				if err = orchestrator.docker.VolumeProvision(ctx, project.InvocationImage, registryAuth, provision); err != nil {
					return fmt.Errorf("volume %s: %w", spec.Name, err)
				}
			}
			if err = orchestrator.docker.VolumeCreate(ctx, spec); err != nil {
				return err
			}
			events.Emit(progress.Event{Group: "volumes", Item: spec.Name, State: progress.Done, Message: "created"})
			continue
		case !docker.SameVolume(existing, spec):
			return fmt.Errorf("volume %s exists with other driver options than the bundle declares: change them back, or remove the volume and its data yourself", spec.Name)
		}
		events.Emit(progress.Event{Group: "volumes", Item: spec.Name, State: progress.Done})
	}
	return nil
}

func (orchestrator *Orchestrator) createNetworks(ctx context.Context, networks []docker.NetworkSpec, events progress.Sink) error {
	for _, spec := range networks {
		existing, err := orchestrator.docker.NetworkFind(ctx, spec.Name)
		if err != nil {
			return err
		}
		message := ""
		if existing == nil {
			if err = orchestrator.docker.NetworkCreate(ctx, spec); err != nil {
				return err
			}
			message = "created"
		}
		events.Emit(progress.Event{Group: "networks", Item: spec.Name, State: progress.Done, Message: message})
	}
	return nil
}

// createConfigs creates the configs that do not exist. One that exists has
// the right content already, since it is named after it: only its labels
// may be out of date, the bundle's version among them, and are updated when
// it is the installation's.
func (orchestrator *Orchestrator) createConfigs(ctx context.Context, project *installer.Project, configs []*swarm.ConfigSpec, events progress.Sink) error {
	want := installationLabels(project.Installation)
	for _, spec := range configs {
		existing, err := orchestrator.docker.ConfigFind(ctx, spec.Name)
		message := ""
		switch {
		case err != nil:
			return err
		case existing == nil:
			if _, err = orchestrator.docker.ConfigCreate(ctx, spec); err != nil {
				return err
			}
			message = "created"
		case docker.Owns(existing.Spec.Labels, want) && !maps.Equal(existing.Spec.Labels, spec.Labels):
			if err = orchestrator.docker.ConfigUpdate(ctx, existing, spec); err != nil {
				return err
			}
		}
		events.Emit(progress.Event{Group: "configs", Item: spec.Name, State: progress.Done, Message: message})
	}
	return nil
}

// createSecrets creates the secrets that do not exist. One that exists has
// the right content already, since it is named after it: only its labels
// may be out of date, the bundle's version among them, and are updated when
// it is the installation's.
func (orchestrator *Orchestrator) createSecrets(ctx context.Context, project *installer.Project, secrets []*swarm.SecretSpec, events progress.Sink) error {
	want := installationLabels(project.Installation)
	for _, spec := range secrets {
		existing, err := orchestrator.docker.SecretFind(ctx, spec.Name)
		message := ""
		switch {
		case err != nil:
			return err
		case existing == nil:
			if _, err = orchestrator.docker.SecretCreate(ctx, spec); err != nil {
				return err
			}
			message = "created"
		case docker.Owns(existing.Spec.Labels, want) && !maps.Equal(existing.Spec.Labels, spec.Labels):
			if err = orchestrator.docker.SecretUpdate(ctx, existing, spec); err != nil {
				return err
			}
		}
		events.Emit(progress.Event{Group: "secrets", Item: spec.Name, State: progress.Done, Message: message})
	}
	return nil
}

// createServices creates or updates the services of each level whose spec
// changed, then waits until they converged before the next level: Swarm
// schedules services on its own, so depends_on is honoured that way.
func (orchestrator *Orchestrator) createServices(ctx context.Context, project *installer.Project, specs *projectSpecs, events progress.Sink) error {
	since := time.Now()
	for _, level := range project.Levels {
		ids := make([]string, len(level))
		for i, name := range level {
			id, change, err := orchestrator.createService(ctx, project, specs.services[name])
			if err != nil {
				return fmt.Errorf("service %s: %w", name, err)
			}
			ids[i] = id
			events.Emit(progress.Event{Group: "services", Item: name, State: progress.Working, Message: change})
		}

		group, groupContext := errgroup.WithContext(ctx)
		for i, name := range level {
			group.Go(func() error {
				report := func(running, desired uint64, phase string) {
					events.Emit(progress.Event{Group: "services", Item: name, State: progress.Working, Current: int64(running), Total: int64(desired), Message: phase})
				}
				if err := orchestrator.converge(groupContext, ids[i], since, report); err != nil {
					events.Emit(progress.Event{Group: "services", Item: name, State: progress.Error, Message: err.Error()})
					return fmt.Errorf("service %s: %w", name, err)
				}
				events.Emit(progress.Event{Group: "services", Item: name, State: progress.Done, Message: "running"})
				return nil
			})
		}
		if err := group.Wait(); err != nil {
			return err
		}
	}
	return nil
}

// createService creates the service, or updates it when its hash is not
// the spec's, and returns its ID and what it did: created, updated or
// unchanged. Swarm gets the credentials of the image's registry for the
// nodes that pull it.
func (orchestrator *Orchestrator) createService(ctx context.Context, project *installer.Project, spec *swarm.ServiceSpec) (string, string, error) {
	if err := orchestrator.resolve(ctx, spec); err != nil {
		return "", "", err
	}
	existing, err := orchestrator.docker.ServiceFind(ctx, spec.Name)
	if err != nil {
		return "", "", err
	}
	registryAuth, err := project.RegistryAuth.For(spec.TaskTemplate.ContainerSpec.Image)
	if err != nil {
		return "", "", err
	}
	spec.Labels[labels.Revision] = project.Installation.Revision
	switch {
	case existing == nil:
		id, err := orchestrator.docker.ServiceCreate(ctx, spec, registryAuth)
		return id, "created", err
	case existing.Spec.Labels[labels.Hash] != spec.Labels[labels.Hash]:
		return existing.ID, "updated", orchestrator.docker.ServiceUpdate(ctx, existing, spec, registryAuth)
	}
	return existing.ID, "unchanged", nil
}

// resolve fills the IDs of the configs and secrets a service uses, external ones included.
func (orchestrator *Orchestrator) resolve(ctx context.Context, spec *swarm.ServiceSpec) error {
	for _, reference := range spec.TaskTemplate.ContainerSpec.Configs {
		config, err := orchestrator.docker.ConfigFind(ctx, reference.ConfigName)
		if err != nil {
			return err
		}
		if config == nil {
			return fmt.Errorf("config %s does not exist", reference.ConfigName)
		}
		reference.ConfigID = config.ID
	}
	for _, reference := range spec.TaskTemplate.ContainerSpec.Secrets {
		secret, err := orchestrator.docker.SecretFind(ctx, reference.SecretName)
		if err != nil {
			return err
		}
		if secret == nil {
			return fmt.Errorf("secret %s does not exist", reference.SecretName)
		}
		reference.SecretID = secret.ID
	}
	return nil
}

// orphans returns what the installation runs that the project no longer
// declares: the services of compose services it dropped, and the configs
// and secrets of contents it no longer has.
func (orchestrator *Orchestrator) orphans(ctx context.Context, project *installer.Project, specs *projectSpecs) (objects, error) {
	var found objects
	selector := docker.ByInstallation(project.Installation)

	services, err := orchestrator.docker.ServiceList(ctx, selector)
	if err != nil {
		return found, err
	}
	for _, service := range services {
		if _, declared := specs.services[service.Spec.Labels[labels.Service]]; !declared {
			found.services = append(found.services, service)
		}
	}

	configs, err := orchestrator.docker.ConfigList(ctx, selector)
	if err != nil {
		return found, err
	}
	for _, config := range configs {
		if !slices.ContainsFunc(specs.configs, func(spec *swarm.ConfigSpec) bool { return spec.Name == config.Spec.Name }) {
			found.configs = append(found.configs, config)
		}
	}

	secrets, err := orchestrator.docker.SecretList(ctx, selector)
	if err != nil {
		return found, err
	}
	for _, secret := range secrets {
		if !slices.ContainsFunc(specs.secrets, func(spec *swarm.SecretSpec) bool { return spec.Name == secret.Spec.Name }) {
			found.secrets = append(found.secrets, secret)
		}
	}

	return found, nil
}
