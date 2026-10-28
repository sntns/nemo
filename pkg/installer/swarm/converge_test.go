package swarm

import (
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

func TestVerdictOf(t *testing.T) {
	before := time.Now()
	later, earlier := before.Add(time.Second), before.Add(-time.Hour)
	own := ownUpdate{changed: true, after: before}
	running := task(swarm.TaskStateRunning, swarm.TaskStateRunning)
	starting := task(swarm.TaskStateStarting, swarm.TaskStateRunning)
	complete := task(swarm.TaskStateComplete, swarm.TaskStateComplete)
	two := uint64(2)

	for name, testCase := range map[string]struct {
		service  swarm.Service
		tasks    []swarm.Task
		verdict  verdict
		rollback string
	}{
		"all running":         {replicated(2, 2), []swarm.Task{running, running}, converged, ""},
		"one still starting":  {replicated(1, 2), []swarm.Task{running, starting}, progressing, ""},
		"old task still runs": {replicated(2, 1), []swarm.Task{running}, progressing, ""},
		"scaled to zero":      {replicated(0, 0), nil, converged, ""},
		"no status yet":       {swarm.Service{}, nil, progressing, ""},
		"updating": {
			func() swarm.Service {
				service := replicated(1, 1)
				service.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStateUpdating, StartedAt: &later}
				return service
			}(),
			[]swarm.Task{running}, progressing, "",
		},
		"rolled back": {
			func() swarm.Service {
				service := replicated(1, 1)
				service.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStateRollbackCompleted, StartedAt: &later, Message: "task failed"}
				return service
			}(),
			[]swarm.Task{running}, rolledBack, "task failed",
		},
		"rollback from an earlier update": {
			func() swarm.Service {
				service := replicated(1, 1)
				service.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStateRollbackCompleted, StartedAt: &earlier}
				return service
			}(),
			[]swarm.Task{running}, converged, "",
		},
		"paused": {
			func() swarm.Service {
				service := replicated(1, 1)
				service.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStatePaused, StartedAt: &later}
				return service
			}(),
			nil, progressing, "",
		},
		"paused, then restarted": {
			func() swarm.Service {
				service := replicated(1, 1)
				service.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStatePaused, StartedAt: &later}
				return service
			}(),
			[]swarm.Task{running}, converged, "",
		},
		"restarting a failed task": {
			replicated(0, 1),
			[]swarm.Task{failedTask(1, swarm.TaskStateShutdown), {Slot: 1, Status: swarm.TaskStatus{State: swarm.TaskStateStarting}, DesiredState: swarm.TaskStateRunning}}, progressing, "",
		},
		"restarting a failed task, not scheduled yet": {
			replicated(0, 1),
			[]swarm.Task{
				{Slot: 1, NodeID: "node", Status: swarm.TaskStatus{State: swarm.TaskStateFailed}, DesiredState: swarm.TaskStateShutdown},
				{Slot: 1, Status: swarm.TaskStatus{State: swarm.TaskStateNew}, DesiredState: swarm.TaskStateReady},
			}, progressing, "",
		},
		"restarting a failed task on another node": {
			replicated(0, 1),
			[]swarm.Task{
				{Slot: 1, NodeID: "node", Status: swarm.TaskStatus{State: swarm.TaskStateFailed}, DesiredState: swarm.TaskStateShutdown},
				{Slot: 1, NodeID: "other", Status: swarm.TaskStatus{State: swarm.TaskStateStarting}, DesiredState: swarm.TaskStateRunning},
			}, progressing, "",
		},
		"restart policy gave up on one node of a global service": {
			swarm.Service{Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{Global: &swarm.GlobalService{}}}},
			[]swarm.Task{
				{NodeID: "node", Status: swarm.TaskStatus{State: swarm.TaskStateFailed}, DesiredState: swarm.TaskStateShutdown},
				{NodeID: "other", Status: swarm.TaskStatus{State: swarm.TaskStateRunning}, DesiredState: swarm.TaskStateRunning},
			}, gaveUp, "",
		},
		"failed task not restarted yet": {
			replicated(0, 1),
			[]swarm.Task{failedTask(1, swarm.TaskStateRunning)}, progressing, "",
		},
		"restart policy gave up": {
			replicated(0, 1),
			[]swarm.Task{failedTask(1, swarm.TaskStateShutdown), failedTask(1, swarm.TaskStateShutdown)}, gaveUp, "",
		},
		"restart policy gave up on one slot": {
			replicated(1, 2),
			[]swarm.Task{{Slot: 1, Status: swarm.TaskStatus{State: swarm.TaskStateRunning}, DesiredState: swarm.TaskStateRunning}, failedTask(2, swarm.TaskStateShutdown)},
			gaveUp, "",
		},
		"replicated job done": {
			swarm.Service{Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{ReplicatedJob: &swarm.ReplicatedJob{TotalCompletions: &two}}}},
			[]swarm.Task{complete, complete}, converged, "",
		},
		"replicated job running": {
			swarm.Service{Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{ReplicatedJob: &swarm.ReplicatedJob{TotalCompletions: &two}}}},
			[]swarm.Task{complete, running}, progressing, "",
		},
		"global job done": {
			swarm.Service{Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{GlobalJob: &swarm.GlobalJob{}}}},
			[]swarm.Task{complete}, converged, "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			verdict, rollback := verdictOf(testCase.service, testCase.tasks, own)
			assert.Equal(t, testCase.verdict, verdict)
			assert.Equal(t, testCase.rollback, rollback)
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

func TestSteadiness(t *testing.T) {
	start := time.Now()
	var steady steadiness
	assert.False(t, steady.held(start, 5*time.Second), "just converged")
	assert.False(t, steady.held(start.Add(4*time.Second), 5*time.Second))
	assert.True(t, steady.held(start.Add(5*time.Second), 5*time.Second), "converged for the monitor period")
	assert.True(t, (&steadiness{}).held(start, 0), "a job has no monitor period")
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

func TestLastError(t *testing.T) {
	older, newer := failedTask(1, swarm.TaskStateShutdown), failedTask(1, swarm.TaskStateShutdown)
	older.Status.Err = "task: non-zero exit (2)"
	newer.CreatedAt = time.Now()
	running := task(swarm.TaskStateRunning, swarm.TaskStateRunning)

	assert.Equal(t, "task: non-zero exit (1)", lastError([]swarm.Task{newer, older, running}), "the newest error")
	assert.Empty(t, lastError([]swarm.Task{running}))
}

// The phase shown is verifying once converged, else why the last task failed, else the state of
// the least advanced task.
func TestObserve(t *testing.T) {
	running := task(swarm.TaskStateRunning, swarm.TaskStateRunning)
	running.Slot = 1
	starting := task(swarm.TaskStateStarting, swarm.TaskStateRunning)
	starting.Slot = 1
	failed := failedTask(1, swarm.TaskStateShutdown)

	assert.Equal(t, "verifying", observe(replicated(1, 1), []swarm.Task{running, failed}, ownUpdate{}).phase)
	assert.Equal(t, "restarting: task: non-zero exit (1)", observe(replicated(0, 1), []swarm.Task{starting, failed}, ownUpdate{}).phase)
	assert.Equal(t, "starting", observe(replicated(0, 1), []swarm.Task{starting}, ownUpdate{}).phase)
}
