// Package swarm is the Swarm orchestrator: it applies a compose project to a
// swarm as services, the way docker stack deploy does, and waits until they
// converged.
package swarm

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/volume"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/installer/swarm/convert"
	"github.com/sntns/nemo/pkg/labels"
)

// Name is the value of the nemo-orchestrator parameter that selects Swarm.
const Name = "swarm"

// Orchestrator applies compose projects to the swarm a manager's daemon runs.
type Orchestrator struct {
	docker *Client
}

// New connects to the Docker of the target, which must be a manager of an
// active swarm.
func New(ctx context.Context) (installer.Orchestrator, error) {
	client, err := connect(ctx)
	if err != nil {
		return nil, err
	}
	return &Orchestrator{docker: client}, nil
}

var _ installer.NewOrchestratorFunc = New

func (orchestrator *Orchestrator) Name() string { return Name }

func (orchestrator *Orchestrator) Close() error { return orchestrator.docker.Close() }

// projectSpecs holds everything Install creates, and Uninstall deletes. It is
// built before anything is created, so that a project Swarm can't honour
// fails without touching the swarm.
type projectSpecs struct {
	services map[string]*swarm.ServiceSpec
	networks []docker.NetworkSpec
	volumes  []*volume.Volume
	configs  []*swarm.ConfigSpec
	secrets  []*swarm.SecretSpec
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
	namespace := convert.NewNamespace(compose.Name)
	base := convert.AddStackLabel(namespace, labels.Merge(project.Labels, map[string]string{labels.Orchestrator: Name}))
	specs := &projectSpecs{services: map[string]*swarm.ServiceSpec{}}

	configs := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(compose.Configs)) {
		spec, err := convert.Config(key, compose.Configs[key], base)
		if err != nil {
			return nil, fmt.Errorf("configs.%s: %w", key, err)
		}
		configs[key] = spec.Name
		if spec.Data != nil {
			specs.configs = append(specs.configs, spec)
		}
	}
	secrets := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(compose.Secrets)) {
		spec, err := convert.Secret(key, compose.Secrets[key], base)
		if err != nil {
			return nil, fmt.Errorf("secrets.%s: %w", key, err)
		}
		secrets[key] = spec.Name
		if spec.Data != nil {
			specs.secrets = append(specs.secrets, spec)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(compose.Volumes)) {
		if volume := compose.Volumes[key]; !bool(volume.External) {
			specs.volumes = append(specs.volumes, convert.Volume(volume, base))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(compose.Networks)) {
		if network := compose.Networks[key]; !bool(network.External) {
			spec, err := convert.Network(network, base)
			if err != nil {
				return nil, fmt.Errorf("networks.%s: %w", key, err)
			}
			specs.networks = append(specs.networks, spec)
		}
	}
	for name, service := range compose.Services {
		spec, err := convert.Service(namespace, compose, service, configs, secrets, base)
		if err != nil {
			return nil, fmt.Errorf("services.%s: %w", name, err)
		}
		specs.services[name] = spec
	}
	return specs, nil
}

// stackLabels are the labels of what Swarm created for the stack. Networks
// are shared by the bundles of a stack, so the bundle does not matter:
// Install reuses, and Uninstall deletes, those that carry them. Volumes are
// the stack's alone: see installer.VolumeOwner.
func stackLabels(installation installer.Installation) map[string]string {
	return map[string]string{labels.Stack: installation.Stack, labels.Orchestrator: Name}
}

// installationLabels are the labels of what Swarm created for the
// installation alone: its services, configs and secrets.
func installationLabels(installation installer.Installation) map[string]string {
	return labels.Merge(stackLabels(installation), map[string]string{labels.Bundle: installation.Bundle})
}
