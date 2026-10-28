package nemo

import (
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
	"github.com/stretchr/testify/assert"

	"github.com/sntns/nemo/pkg/labels"
)

func TestSwarmState(t *testing.T) {
	t.Parallel()
	service := func(running, desired uint64) swarm.Service {
		return swarm.Service{
			Spec:          swarm.ServiceSpec{Annotations: swarm.Annotations{Labels: map[string]string{labels.Service: "web"}}},
			ServiceStatus: &swarm.ServiceStatus{RunningTasks: running, DesiredTasks: desired},
		}
	}

	assert.Equal(t, ServiceState{Name: "web", Running: 2, Desired: 2, State: "running"}, swarmState(service(2, 2)))
	assert.Equal(t, ServiceState{Name: "web", Running: 1, Desired: 2, State: "starting"}, swarmState(service(1, 2)))
	assert.Equal(t, ServiceState{Name: "web", State: "running"}, swarmState(swarm.Service{Spec: service(0, 0).Spec}), "no status yet")
}

func TestComposeState(t *testing.T) {
	t.Parallel()
	running := container.Summary{State: container.StateRunning}
	healthy := container.Summary{State: container.StateRunning, Health: &container.HealthSummary{Status: container.Healthy}}
	unhealthy := container.Summary{State: container.StateRunning, Health: &container.HealthSummary{Status: container.Unhealthy}}
	exited := container.Summary{State: container.StateExited}

	assert.Equal(t, ServiceState{Name: "web", Running: 2, Desired: 2, State: "running"}, composeState("web", []container.Summary{running, healthy}))
	assert.Equal(t, ServiceState{Name: "web", Running: 1, Desired: 2, State: "exited"}, composeState("web", []container.Summary{running, exited}))
	assert.Equal(t, ServiceState{Name: "web", Running: 1, Desired: 1, State: "unhealthy"}, composeState("web", []container.Summary{unhealthy}), "running, but unhealthy")
}
