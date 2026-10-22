// Package compose is the Compose orchestrator: it applies a compose project
// to one Docker as plain containers, the way docker compose up --wait does,
// and labels them the way docker compose does, so that docker compose ps
// and logs still see them.
//
// It drives the daemon with the moby client, not with the docker/compose
// packages, which pull the legacy docker/docker and docker/cli modules.
package compose

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/moby/moby/api/types/volume"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/installer/compose/convert"
	"github.com/sntns/nemo/pkg/labels"
)

// Name is the value of the nemo-orchestrator parameter that selects Compose.
const Name = "compose"

// Orchestrator applies compose projects to one Docker.
type Orchestrator struct {
	docker *docker.Client
}

// New connects to the Docker of the target. Compose runs on any Docker, a
// swarm manager included.
func New(ctx context.Context) (installer.Orchestrator, error) {
	client, err := docker.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &Orchestrator{docker: client}, nil
}

var _ installer.NewOrchestratorFunc = New

func (orchestrator *Orchestrator) Name() string { return Name }

func (orchestrator *Orchestrator) Close() error { return orchestrator.docker.Close() }

// projectSpecs holds everything Install creates, and Uninstall deletes. It is
// built before anything is created, so that a project Compose can't honour
// fails without touching Docker.
type projectSpecs struct {
	services map[string]*convert.ServiceSpec
	networks []docker.NetworkSpec
	volumes  []*volume.Volume
}

func (orchestrator *Orchestrator) Validate(project *installer.Project) error {
	if err := project.Validate(); err != nil {
		return err
	}

	if err := validate(project.Compose).Err(); err != nil {
		return err
	}

	_, err := build(project)
	return err
}

func build(project *installer.Project) (*projectSpecs, error) {
	compose := project.Compose
	base := labels.Merge(project.Labels, map[string]string{convert.ProjectLabel: compose.Name, labels.Orchestrator: Name})
	specs := &projectSpecs{services: map[string]*convert.ServiceSpec{}}
	for _, key := range slices.Sorted(maps.Keys(compose.Volumes)) {
		if volume := compose.Volumes[key]; !bool(volume.External) {
			specs.volumes = append(specs.volumes, convert.Volume(key, volume, base))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(compose.Networks)) {
		if network := compose.Networks[key]; !bool(network.External) {
			spec, err := convert.Network(key, network, base)
			if err != nil {
				return nil, fmt.Errorf("networks.%s: %w", key, err)
			}
			specs.networks = append(specs.networks, spec)
		}
	}
	conditions := convert.Conditions(compose)
	for name, service := range compose.Services {
		spec, err := convert.Service(compose, service, base)
		if err != nil {
			return nil, fmt.Errorf("services.%s: %w", name, err)
		}
		spec.Wait = conditions[name]
		specs.services[name] = spec
	}
	return specs, nil
}

// stackLabels are the labels of what Compose created for the stack. Networks
// are shared by the bundles of a stack, so the bundle does not matter:
// Install reuses, and Uninstall deletes, those that carry them. Volumes are
// the stack's alone: see installer.VolumeOwner.
func stackLabels(installation installer.Installation) map[string]string {
	return map[string]string{labels.Stack: installation.Stack, labels.Orchestrator: Name}
}
