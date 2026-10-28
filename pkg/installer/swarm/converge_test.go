package swarm

import (
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/stretchr/testify/assert"
)

func task(state, desired swarm.TaskState) swarm.Task {
	return swarm.Task{Status: swarm.TaskStatus{State: state}, DesiredState: desired}
}

func failedTask(slot int, desired swarm.TaskState) swarm.Task {
	return swarm.Task{Slot: slot, Status: swarm.TaskStatus{State: swarm.TaskStateFailed, Err: "task: non-zero exit (1)"}, DesiredState: desired}
}

func replicated(running, desired uint64) swarm.Service {
	return swarm.Service{
		Spec:          swarm.ServiceSpec{Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{}}},
		ServiceStatus: &swarm.ServiceStatus{RunningTasks: running, DesiredTasks: desired},
	}
}

func TestConvergence(t *testing.T) {
	before := time.Now()
	later, earlier := before.Add(time.Second), before.Add(-time.Hour)
	own := ownUpdate{changed: true, after: before}
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
			nil, false, "",
		},
		"paused, then restarted": {
			func() swarm.Service {
				service := replicated(1, 1)
				service.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStatePaused, StartedAt: &later}
				return service
			}(),
			[]swarm.Task{running}, true, "",
		},
		"restarting a failed task": {
			replicated(0, 1),
			[]swarm.Task{failedTask(1, swarm.TaskStateShutdown), {Slot: 1, Status: swarm.TaskStatus{State: swarm.TaskStateStarting}, DesiredState: swarm.TaskStateRunning}}, false, "",
		},
		"restarting a failed task, not scheduled yet": {
			replicated(0, 1),
			[]swarm.Task{
				{Slot: 1, NodeID: "node", Status: swarm.TaskStatus{State: swarm.TaskStateFailed}, DesiredState: swarm.TaskStateShutdown},
				{Slot: 1, Status: swarm.TaskStatus{State: swarm.TaskStateNew}, DesiredState: swarm.TaskStateReady},
			}, false, "",
		},
		"restarting a failed task on another node": {
			replicated(0, 1),
			[]swarm.Task{
				{Slot: 1, NodeID: "node", Status: swarm.TaskStatus{State: swarm.TaskStateFailed}, DesiredState: swarm.TaskStateShutdown},
				{Slot: 1, NodeID: "other", Status: swarm.TaskStatus{State: swarm.TaskStateStarting}, DesiredState: swarm.TaskStateRunning},
			}, false, "",
		},
		"restart policy gave up on one node of a global service": {
			swarm.Service{Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{Global: &swarm.GlobalService{}}}},
			[]swarm.Task{
				{NodeID: "node", Status: swarm.TaskStatus{State: swarm.TaskStateFailed}, DesiredState: swarm.TaskStateShutdown},
				{NodeID: "other", Status: swarm.TaskStatus{State: swarm.TaskStateRunning}, DesiredState: swarm.TaskStateRunning},
			}, false, "stopped restarting",
		},
		"failed task not restarted yet": {
			replicated(0, 1),
			[]swarm.Task{failedTask(1, swarm.TaskStateRunning)}, false, "",
		},
		"restart policy gave up": {
			replicated(0, 1),
			[]swarm.Task{failedTask(1, swarm.TaskStateShutdown), failedTask(1, swarm.TaskStateShutdown)}, false, "stopped restarting",
		},
		"restart policy gave up on one slot": {
			replicated(1, 2),
			[]swarm.Task{{Slot: 1, Status: swarm.TaskStatus{State: swarm.TaskStateRunning}, DesiredState: swarm.TaskStateRunning}, failedTask(2, swarm.TaskStateShutdown)},
			false, "stopped restarting",
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
			done, err := convergence(testCase.service, testCase.tasks, own)
			if done != testCase.done || (err == nil) != (testCase.err == "") || (err != nil && !strings.Contains(err.Error(), testCase.err)) {
				t.Errorf("got %v, %v; want %v, %q", done, err, testCase.done, testCase.err)
			}
		})
	}
}

// nemo's update is any Swarm started after the one it showed before nemo's change, on the
// manager's clock: the installer's clock, here an hour ahead, does not count.
func TestOwnUpdate(t *testing.T) {
	previous := time.Now().Add(-2 * time.Hour)
	next := previous.Add(time.Minute)
	status := func(startedAt time.Time) *swarm.UpdateStatus {
		return &swarm.UpdateStatus{State: swarm.UpdateStateRollbackCompleted, StartedAt: &startedAt}
	}

	updated := ownUpdate{changed: true, after: previous}
	assert.True(t, updated.of(status(next)), "the rollback of nemo's update")
	assert.False(t, updated.of(status(previous)), "the rollback of the update before")
	assert.False(t, updated.of(nil), "no update yet")
	assert.True(t, ownUpdate{changed: true}.of(status(next)), "a service that had no update before")
	assert.False(t, ownUpdate{}.of(status(next)), "a service nemo left as it was")
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

func TestLastFailure(t *testing.T) {
	older, newer := failedTask(1, swarm.TaskStateShutdown), failedTask(1, swarm.TaskStateShutdown)
	older.Status.Err = "task: non-zero exit (2)"
	newer.CreatedAt = time.Now()
	running := task(swarm.TaskStateRunning, swarm.TaskStateRunning)
	running.Slot = 1
	if reason, gaveUp := lastFailure([]swarm.Task{newer, older, running}); reason != "task: non-zero exit (1)" || gaveUp {
		t.Errorf("got %q, %v; want the newest error, still restarting", reason, gaveUp)
	}
	if reason, gaveUp := lastFailure([]swarm.Task{running}); reason != "" || gaveUp {
		t.Errorf("got %q, %v; want no failure", reason, gaveUp)
	}
}
