package compose

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/installer/compose/convert"
	"github.com/sntns/nemo/pkg/labels"
	"github.com/sntns/nemo/pkg/progress"
)

// service returns a service of that name, changed by edit: most fields of a
// ServiceConfig are promoted from an embedded struct, so literals can't set them.
func service(name string, edit func(service *types.ServiceConfig)) types.ServiceConfig {
	service := types.ServiceConfig{Name: name}
	if edit != nil {
		edit(&service)
	}
	return service
}

func TestReached(t *testing.T) {
	t.Parallel()

	running := &container.State{Status: container.StateRunning, Running: true}
	starting := &container.State{Status: container.StateRunning, Running: true, Health: &container.Health{Status: container.Starting}}
	unhealthy := &container.State{Status: container.StateRunning, Running: true, Health: &container.Health{Status: container.Unhealthy, Log: []*container.HealthcheckResult{{Output: "connection refused\n"}}}}
	exited0 := &container.State{Status: container.StateExited, ExitCode: 0}
	exited1 := &container.State{Status: container.StateExited, ExitCode: 1}

	for name, testCase := range map[string]struct {
		state  *container.State
		wanted convert.Condition
		done   bool
		err    string
	}{
		"running is started":          {running, convert.Started, true, ""},
		"starting is not yet healthy": {starting, convert.Healthy, false, ""},
		"unhealthy never will be":     {unhealthy, convert.Healthy, false, "unhealthy: connection refused"},
		"running is not completed":    {running, convert.Completed, false, ""},
		"exit 0 is completed":         {exited0, convert.Completed, true, ""},
		"exit 1 never completes":      {exited1, convert.Completed, false, "exited with code 1"},
		"exited is not started":       {exited0, convert.Started, false, "exited with code 0"},
		"no state yet":                {nil, convert.Started, false, ""},
		"running without healthcheck": {running, convert.Healthy, true, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			done, err := reached(testCase.state, testCase.wanted)
			assert.Equal(t, testCase.done, done)
			if testCase.err == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, testCase.err)
			}
		})
	}
}

// A container counts as up once it ran for steadyFor without starting again: one that
// exits, or that the daemon restarts, starts the count over.
func TestSteadiness(t *testing.T) {
	t.Parallel()
	start := time.Now()
	first := &container.State{Running: true, StartedAt: "2026-10-28T00:00:00Z"}
	restarted := &container.State{Running: true, StartedAt: "2026-10-28T00:00:07Z"}

	var steady steadiness
	assert.False(t, steady.held(first, start), "just started")
	assert.False(t, steady.held(first, start.Add(steadyFor-time.Second)), "not long enough")
	assert.True(t, steady.held(first, start.Add(steadyFor)), "up for steadyFor")
	assert.False(t, steady.held(restarted, start.Add(steadyFor+2*time.Second)), "restarted: the count starts over")
	assert.True(t, steady.held(restarted, start.Add(2*steadyFor+2*time.Second)))
}

func TestValidate_swarmSettingsAreWarnings(t *testing.T) {
	t.Parallel()

	project := &types.Project{Services: types.Services{
		"web": service("web", func(service *types.ServiceConfig) {
			service.Image = "nginx"
			service.Deploy = &types.DeployConfig{
				Placement:    types.Placement{Constraints: []string{"node.role==manager"}},
				UpdateConfig: &types.UpdateConfig{},
			}
		}),
	}}
	problems := validate(project)
	require.NoError(t, problems.Err(), "a bundle written for Swarm still runs on Compose")

	var paths []string
	for _, warning := range problems.Warnings() {
		paths = append(paths, warning.Path)
	}
	assert.Equal(t, []string{"services.web.deploy.placement", "services.web.deploy.update_config"}, paths, "and says what it ignores")
}

func TestValidate_errors(t *testing.T) {
	t.Parallel()

	project := &types.Project{
		WorkingDir: "/cnab/app",
		Services: types.Services{
			"web": service("web", func(service *types.ServiceConfig) {
				service.Image = "nginx"
				service.ContainerName = "web"
				service.Volumes = []types.ServiceVolumeConfig{{Type: types.VolumeTypeBind, Source: "/cnab/app/data", Target: "/data"}}
			}),
		},
		Configs: types.Configs{"page": {External: true}},
	}
	err := validate(project).Err()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services.web.container_name")
	assert.Contains(t, err.Error(), "services.web.volumes[0].source: a bind mount needs an absolute path on the host")
	assert.Contains(t, err.Error(), "configs.page.external")
}

func TestOwnership_volumeKeptBySwarm(t *testing.T) {
	t.Parallel()
	keptBySwarm := map[string]string{labels.Stack: "demo", labels.Orchestrator: "swarm", labels.Bundle: "runtime"}
	installation := installer.Installation{Stack: "demo", Bundle: "runtime"}
	assert.True(t, docker.Owns(keptBySwarm, installer.VolumeOwner(installation)), "compose mounts the data swarm kept")
	assert.False(t, docker.Owns(keptBySwarm, stackLabels(installation)), "but not a network swarm created")
	assert.False(t, docker.Owns(keptBySwarm, installer.VolumeOwner(installer.Installation{Stack: "staging"})), "nor another stack's data")
}

// engine is a Docker that has no container yet, and on which every
// container created runs at once.
type engine struct {
	client.APIClient
	created int
}

func (engine *engine) ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error) {
	return client.ContainerListResult{}, nil
}

func (engine *engine) ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	engine.created++
	return client.ContainerCreateResult{ID: fmt.Sprintf("%064d", engine.created)}, nil
}

func (engine *engine) ContainerStart(ctx context.Context, id string, options client.ContainerStartOptions) (client.ContainerStartResult, error) {
	return client.ContainerStartResult{}, nil
}

func (engine *engine) ContainerInspect(ctx context.Context, id string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	state := &container.State{Status: container.StateRunning, Running: true}
	return client.ContainerInspectResult{Container: container.InspectResponse{State: state}}, nil
}

func TestCreateServices_progress(t *testing.T) {
	t.Parallel()
	orchestrator := &Orchestrator{docker: docker.New(&engine{})}
	spec := &convert.ServiceSpec{
		Name:     "web",
		Replicas: 2,
		Config:   &container.Config{Labels: map[string]string{labels.Stack: "demo", labels.Bundle: "nginx"}},
		Wait:     convert.Healthy,
	}
	project := &installer.Project{Levels: [][]string{{"web"}}}
	var events []string
	sink := progress.SinkFunc(func(event progress.Event) {
		events = append(events, fmt.Sprintf("%s %d/%d %s", event.State, event.Current, event.Total, event.Message))
	})

	require.NoError(t, orchestrator.createServices(context.Background(), project, &projectSpecs{services: map[string]*convert.ServiceSpec{"web": spec}}, sink))
	assert.Equal(t, []string{
		"working 0/2 creating",
		"working 0/2 checking health",
		"working 1/2 checking health",
		"done 2/2 healthy",
	}, events, "each container counted once it reached the state its dependants wait for")
}
