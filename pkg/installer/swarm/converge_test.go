package swarm

import (
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
)

func task(state, desired swarm.TaskState) swarm.Task {
	return swarm.Task{Status: swarm.TaskStatus{State: state}, DesiredState: desired}
}

func replicated(running, desired uint64) swarm.Service {
	return swarm.Service{
		Spec:          swarm.ServiceSpec{Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{}}},
		ServiceStatus: &swarm.ServiceStatus{RunningTasks: running, DesiredTasks: desired},
	}
}

func TestConvergence(t *testing.T) {
	since := time.Now()
	later, earlier := since.Add(time.Second), since.Add(-time.Hour)
	running := task(swarm.TaskStateRunning, swarm.TaskStateRunning)
	starting := task(swarm.TaskStateStarting, swarm.TaskStateRunning)
	complete := task(swarm.TaskStateComplete, swarm.TaskStateComplete)
	two := uint64(2)

	for name, testCase := range map[string]struct {
		service swarm.Service
		tasks   []swarm.Task
		done    bool
		err     string
	}{
		"all running":         {replicated(2, 2), []swarm.Task{running, running}, true, ""},
		"one still starting":  {replicated(1, 2), []swarm.Task{running, starting}, false, ""},
		"old task still runs": {replicated(2, 1), []swarm.Task{running}, false, ""},
		"scaled to zero":      {replicated(0, 0), nil, true, ""},
		"no status yet":       {swarm.Service{}, nil, false, ""},
		"updating": {
			func() swarm.Service {
				service := replicated(1, 1)
				service.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStateUpdating, StartedAt: &later}
				return service
			}(),
			[]swarm.Task{running}, false, "",
		},
		"rolled back": {
			func() swarm.Service {
				service := replicated(1, 1)
				service.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStateRollbackCompleted, StartedAt: &later, Message: "task failed"}
				return service
			}(),
			[]swarm.Task{running}, false, "rolled the update back: task failed",
		},
		"rollback from an earlier update": {
			func() swarm.Service {
				service := replicated(1, 1)
				service.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStateRollbackCompleted, StartedAt: &earlier}
				return service
			}(),
			[]swarm.Task{running}, true, "",
		},
		"paused": {
			func() swarm.Service {
				service := replicated(1, 1)
				service.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStatePaused, StartedAt: &later}
				return service
			}(),
			nil, false, "paused the update",
		},
		"replicated job done": {
			swarm.Service{Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{ReplicatedJob: &swarm.ReplicatedJob{TotalCompletions: &two}}}},
			[]swarm.Task{complete, complete}, true, "",
		},
		"replicated job running": {
			swarm.Service{Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{ReplicatedJob: &swarm.ReplicatedJob{TotalCompletions: &two}}}},
			[]swarm.Task{complete, running}, false, "",
		},
		"global job done": {
			swarm.Service{Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{GlobalJob: &swarm.GlobalJob{}}}},
			[]swarm.Task{complete}, true, "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			done, err := convergence(testCase.service, testCase.tasks, since)
			if done != testCase.done || (err == nil) != (testCase.err == "") || (err != nil && !strings.Contains(err.Error(), testCase.err)) {
				t.Errorf("got %v, %v; want %v, %q", done, err, testCase.done, testCase.err)
			}
		})
	}
}

func TestTasksOf(t *testing.T) {
	preparing := task(swarm.TaskStatePreparing, swarm.TaskStateRunning)
	running := task(swarm.TaskStateRunning, swarm.TaskStateRunning)
	stopping := task(swarm.TaskStateNew, swarm.TaskStateShutdown)

	count, desired, phase := tasksOf(replicated(1, 3), []swarm.Task{running, preparing, stopping})
	if count != 1 || desired != 3 || phase != "preparing" {
		t.Errorf("1 of 3 running, the least advanced preparing: got %d of %d, %s", count, desired, phase)
	}
	if _, _, phase = tasksOf(replicated(3, 3), []swarm.Task{running, running, running}); phase != "running" {
		t.Errorf("all running: got %s", phase)
	}
}
