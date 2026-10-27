package convert

import (
	"fmt"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/swarm"

	"github.com/sntns/nemo/pkg/docker"
)

func serviceMode(service types.ServiceConfig, deploy *types.DeployConfig) (swarm.ServiceMode, error) {
	var replicas *uint64
	if number := deploy.Replicas; number != nil || service.Scale != nil {
		if number == nil {
			number = service.Scale
		}
		count := uint64(*number)
		replicas = &count
	}
	switch deploy.Mode {
	case "replicated", "":
		return swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: replicas}}, nil
	case "replicated-job":
		return swarm.ServiceMode{ReplicatedJob: &swarm.ReplicatedJob{MaxConcurrent: replicas, TotalCompletions: replicas}}, nil
	case "global", "global-job":
		if replicas != nil {
			return swarm.ServiceMode{}, fmt.Errorf("replicas can't be set in %s mode", deploy.Mode)
		}
		if deploy.Mode == "global" {
			return swarm.ServiceMode{Global: &swarm.GlobalService{}}, nil
		}
		return swarm.ServiceMode{GlobalJob: &swarm.GlobalJob{}}, nil
	}
	return swarm.ServiceMode{}, fmt.Errorf("unknown deploy mode %q", deploy.Mode)
}

func resources(source types.Resources) *swarm.ResourceRequirements {
	result := &swarm.ResourceRequirements{}
	if limits := source.Limits; limits != nil {
		result.Limits = &swarm.Limit{NanoCPUs: docker.NanoCPUs(limits.NanoCPUs.Value()), MemoryBytes: int64(limits.MemoryBytes), Pids: limits.Pids}
	}
	if reservations := source.Reservations; reservations != nil {
		result.Reservations = &swarm.Resources{NanoCPUs: docker.NanoCPUs(reservations.NanoCPUs.Value()), MemoryBytes: int64(reservations.MemoryBytes)}
		for _, generic := range reservations.GenericResources {
			if generic.DiscreteResourceSpec != nil {
				result.Reservations.GenericResources = append(result.Reservations.GenericResources, swarm.GenericResource{
					DiscreteResourceSpec: &swarm.DiscreteGenericResource{Kind: generic.DiscreteResourceSpec.Kind, Value: generic.DiscreteResourceSpec.Value},
				})
			}
		}
	}
	return result
}

func placement(service types.ServiceConfig, source types.Placement) (*swarm.Placement, error) {
	result := &swarm.Placement{Constraints: source.Constraints, MaxReplicas: source.MaxReplicas}
	for _, preference := range source.Preferences {
		result.Preferences = append(result.Preferences, swarm.PlacementPreference{Spread: &swarm.SpreadOver{SpreadDescriptor: preference.Spread}})
	}
	if service.Platform != "" {
		operatingSystem, architecture, ok := strings.Cut(service.Platform, "/")
		if !ok {
			return nil, fmt.Errorf("platform %q: expected os/architecture", service.Platform)
		}
		architecture, _, _ = strings.Cut(architecture, "/")
		result.Platforms = []swarm.Platform{{OS: operatingSystem, Architecture: architecture}}
	}
	return result, nil
}

func restartPolicy(source *types.RestartPolicy) *swarm.RestartPolicy {
	if source == nil {
		return nil
	}
	return &swarm.RestartPolicy{
		Condition:   swarm.RestartPolicyCondition(source.Condition),
		Delay:       (*time.Duration)(source.Delay),
		MaxAttempts: source.MaxAttempts,
		Window:      (*time.Duration)(source.Window),
	}
}

// updateConfig converts update_config and rollback_config, so that the
// update policy is the bundle's, not a forced one.
func updateConfig(source *types.UpdateConfig) *swarm.UpdateConfig {
	if source == nil {
		return nil
	}
	parallelism := uint64(1)
	if source.Parallelism != nil {
		parallelism = *source.Parallelism
	}
	return &swarm.UpdateConfig{
		Parallelism:     parallelism,
		Delay:           time.Duration(source.Delay),
		FailureAction:   swarm.FailureAction(source.FailureAction),
		Monitor:         time.Duration(source.Monitor),
		MaxFailureRatio: source.MaxFailureRatio,
		Order:           swarm.UpdateOrder(source.Order),
	}
}
