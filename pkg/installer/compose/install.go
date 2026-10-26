package compose

import (
	"archive/tar"
	"bytes"
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"golang.org/x/sync/errgroup"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/installer/compose/convert"
	"github.com/sntns/nemo/pkg/labels"
	"github.com/sntns/nemo/pkg/progress"
)

// Install creates what the project declares and is not there yet, the way
// docker compose up --wait does: volumes, networks, then the containers of
// the services, level after level. It then deletes the containers of the
// services the project no longer declares.
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
	if err = orchestrator.pullImages(ctx, project, specs); err != nil {
		return err
	}
	if err = orchestrator.createVolumes(ctx, project, specs.volumes, events); err != nil {
		return err
	}
	if err = orchestrator.createNetworks(ctx, specs.networks, events); err != nil {
		return err
	}
	if err = orchestrator.createServices(ctx, project, specs, events); err != nil {
		return err
	}

	orphans, err := orchestrator.orphans(ctx, project, specs)
	if err != nil {
		return err
	}
	return orchestrator.delete(ctx, orphans, events)
}

// checkOwnership makes sure that the networks and volumes of the project
// that already exist are the stack's, before anything is created.
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
	return nil
}

func (orchestrator *Orchestrator) pullImages(ctx context.Context, project *installer.Project, specs *projectSpecs) error {
	for _, name := range slices.Sorted(maps.Keys(specs.services)) {
		service := project.Compose.Services[name]
		registryAuth, err := project.RegistryAuth.For(service.Image)
		if err != nil {
			return fmt.Errorf("services.%s: %w", name, err)
		}
		if err := orchestrator.docker.ImageEnsure(ctx, service.Image, service.PullPolicy, registryAuth); err != nil {
			return fmt.Errorf("services.%s: %w", name, err)
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
		if existing == nil {
			if err = orchestrator.docker.NetworkCreate(ctx, spec); err != nil {
				return err
			}
		}
		events.Emit(progress.Event{Group: "networks", Item: spec.Name, State: progress.Done})
	}
	return nil
}

// createServices converges the containers of each level of services, then
// waits until they reach the state their dependants ask for before the next
// level starts, counting the containers of each service that reached it.
func (orchestrator *Orchestrator) createServices(ctx context.Context, project *installer.Project, specs *projectSpecs, events progress.Sink) error {
	for _, level := range project.Levels {
		group, groupContext := errgroup.WithContext(ctx)
		for _, name := range level {
			spec := specs.services[name]
			ids, err := orchestrator.createService(ctx, project.Installation.Revision, spec, events)
			if err != nil {
				events.Emit(progress.Event{Group: "services", Item: spec.Name, State: progress.Error, Message: err.Error()})
				return fmt.Errorf("service %s: %w", spec.Name, err)
			}
			group.Go(func() error {
				total := int64(len(ids))
				for ready, id := range ids {
					events.Emit(progress.Event{Group: "services", Item: spec.Name, State: progress.Working, Current: int64(ready), Total: total, Message: waiting(spec.Wait)})
					if err := orchestrator.wait(groupContext, id, spec.Wait); err != nil {
						events.Emit(progress.Event{Group: "services", Item: spec.Name, State: progress.Error, Message: err.Error()})
						return fmt.Errorf("service %s: %w", spec.Name, err)
					}
				}
				events.Emit(progress.Event{Group: "services", Item: spec.Name, State: progress.Done, Current: total, Total: total, Message: string(spec.Wait)})
				return nil
			})
		}
		if err := group.Wait(); err != nil {
			return err
		}
	}
	return nil
}

// createService keeps the containers whose hash is the spec's, starting them
// when stopped, recreates the others, and deletes those beyond the replicas.
func (orchestrator *Orchestrator) createService(ctx context.Context, revision string, spec *convert.ServiceSpec, events progress.Sink) ([]string, error) {
	existing, err := orchestrator.containers(ctx, spec)
	if err != nil {
		return nil, err
	}
	byNumber := map[int]container.Summary{}
	for _, summary := range existing {
		number, _ := strconv.Atoi(summary.Labels[convert.NumberLabel])
		byNumber[number] = summary
	}

	events.Emit(progress.Event{Group: "services", Item: spec.Name, State: progress.Working, Total: int64(spec.Replicas), Message: "creating"})
	var ids []string
	for number := 1; number <= spec.Replicas; number++ {
		summary, found := byNumber[number]
		delete(byNumber, number)
		id := summary.ID
		switch {
		case found && summary.Labels[labels.Hash] == spec.Hash && summary.State == container.StateRunning:
		// A container that never started, left by a failed start, is created again: Docker Desktop publishes no port on its next start.
		case found && summary.Labels[labels.Hash] == spec.Hash && summary.State != container.StateCreated:
			if err = orchestrator.docker.ContainerStart(ctx, id); err != nil {
				return nil, err
			}
		default:
			if found {
				if err = orchestrator.docker.ContainerDelete(ctx, id); err != nil {
					return nil, err
				}
			}
			if id, err = orchestrator.createContainer(ctx, revision, spec, number); err != nil {
				return nil, err
			}
		}
		ids = append(ids, id)
	}
	for _, summary := range byNumber {
		if err = orchestrator.docker.ContainerDelete(ctx, summary.ID); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// createContainer creates the container of a replica, puts its configs and
// secrets in it, and starts it.
func (orchestrator *Orchestrator) createContainer(ctx context.Context, revision string, spec *convert.ServiceSpec, number int) (string, error) {
	config := *spec.Config
	config.Labels = labels.Merge(spec.Config.Labels, map[string]string{
		convert.NumberLabel:     strconv.Itoa(number),
		convert.ConfigHashLabel: spec.Hash,
		labels.Hash:             spec.Hash,
		labels.Revision:         revision,
	})
	id, err := orchestrator.docker.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:             convert.ContainerName(spec.Config.Labels[labels.Stack], spec.Name, number),
		Config:           &config,
		HostConfig:       spec.Host,
		NetworkingConfig: spec.Networking,
		Platform:         spec.Platform,
	})
	if err != nil {
		return "", err
	}
	if err = orchestrator.copyFiles(ctx, id, spec.Files); err != nil {
		return "", err
	}
	if err = orchestrator.docker.ContainerStart(ctx, id); err != nil {
		return "", err
	}
	return id, nil
}

// copyFiles puts the configs and secrets in the container before it starts, as docker compose does:
// a bind from the installer would point inside the installer, not on the host.
func (orchestrator *Orchestrator) copyFiles(ctx context.Context, id string, files []convert.File) error {
	if len(files) == 0 {
		return nil
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for _, file := range files {
		header := &tar.Header{Name: strings.TrimPrefix(file.Path, "/"), Mode: int64(file.Mode), Size: int64(len(file.Data)), Uid: file.UID, Gid: file.GID}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if _, err := writer.Write(file.Data); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return orchestrator.docker.ContainerCopy(ctx, id, &archive)
}

// orphans returns the containers of the services the project no longer
// declares.
func (orchestrator *Orchestrator) orphans(ctx context.Context, project *installer.Project, specs *projectSpecs) (objects, error) {
	var found objects
	containers, err := orchestrator.docker.ContainerList(ctx, docker.ByInstallation(project.Installation))
	if err != nil {
		return found, err
	}
	for _, summary := range containers {
		if _, declared := specs.services[summary.Labels[labels.Service]]; !declared {
			found.containers = append(found.containers, summary)
		}
	}
	return found, nil
}
