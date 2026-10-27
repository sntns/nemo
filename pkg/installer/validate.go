package installer

import (
	"maps"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

const unsupported = "not supported"

// Validate reports what of the project no orchestrator can honour: what
// needs a build, a model provider or lifecycle hooks, and dependencies
// that can never be met.
func (project *Project) Validate() error {
	problems := NewProblems()
	compose := project.Compose
	problems.At("models").AddIf(len(compose.Models) > 0, unsupported)
	for _, name := range slices.Sorted(maps.Keys(compose.Services)) {
		service := compose.Services[name]
		at := problems.At("services", name)
		at.At("image").AddIf(service.Image == "", "required: nemo deploys images, it does not build them")
		at.At("build").AddIf(service.Build != nil, "not supported: nemo deploys images, it does not build them")
		at.At("develop").AddIf(service.Develop != nil, unsupported)
		at.At("provider").AddIf(service.Provider != nil, unsupported)
		at.At("models").AddIf(len(service.Models) > 0, unsupported)
		at.At("pre_start").AddIf(len(service.PreStart) > 0, unsupported)
		at.At("post_start").AddIf(len(service.PostStart) > 0, unsupported)
		at.At("pre_stop").AddIf(len(service.PreStop) > 0, unsupported)
		validateDependencies(at, compose, service)
	}
	return problems.Err()
}

func validateDependencies(problems Problems, compose *types.Project, service types.ServiceConfig) {
	for _, name := range slices.Sorted(maps.Keys(service.DependsOn)) {
		dependency := service.DependsOn[name]
		at := problems.At("depends_on", name)
		target, declared := compose.Services[name]
		at.AddIf(!declared, "service is not declared")
		at.AddIf(
			declared &&
				dependency.Condition == types.ServiceConditionHealthy &&
				(target.HealthCheck == nil || target.HealthCheck.Disable),
			"service_healthy needs a healthcheck on "+name,
		)
		at.At("restart").AddIf(dependency.Restart && !derived(service, name), unsupported)
	}
}

// derived tells whether the loader added the dependency itself, with its
// restart, for a link or a namespace the service shares: each orchestrator
// reports those for what they are.
func derived(service types.ServiceConfig, name string) bool {
	for _, link := range service.Links {
		if target, _, _ := strings.Cut(link, ":"); target == name {
			return true
		}
	}
	return slices.Contains([]string{service.NetworkMode, service.Ipc, service.Pid, service.Uts, service.Cgroup}, types.ServicePrefix+name)
}
