package swarm

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
)

// pollInterval is how often converge looks at a service.
const pollInterval = 500 * time.Millisecond

// converge waits until the service has converged and stays so for its update monitor period, the
// way docker service update does: a task that starts then crashes is caught. It reports its
// running and desired tasks and the state of the least advanced, when they change.
func (orchestrator *Orchestrator) converge(ctx context.Context, id string, own ownUpdate, report func(running, desired uint64, phase string)) error {
	var steady steadiness
	var reported string
	for {
		service, tasks, err := orchestrator.serviceState(ctx, id)
		if err != nil {
			return err
		}
		look := observe(service, tasks, own)
		switch look.verdict {
		case rolledBack:
			return orchestrator.withTaskError(ctx, id, fmt.Errorf("swarm rolled the update back: %s", look.rollback))
		case gaveUp:
			return orchestrator.withTaskError(ctx, id, errors.New("swarm stopped restarting its task, as its restart policy says"))
		case converged:
			if steady.held(time.Now(), monitor(service)) {
				return nil
			}
		case progressing:
			steady = steadiness{}
		}
		if shown := fmt.Sprint(look.running, look.desired, look.phase); shown != reported {
			report(look.running, look.desired, look.phase)
			reported = shown
		}
		select {
		case <-ctx.Done():
			return orchestrator.withTaskError(context.WithoutCancel(ctx), id, fmt.Errorf("not converged in time: %w", ctx.Err()))
		case <-time.After(pollInterval):
		}
	}
}

// steadiness follows a service that converged: it holds once the service stayed so for its
// monitor period.
type steadiness struct {
	since time.Time
}

func (steady *steadiness) held(now time.Time, period time.Duration) bool {
	if steady.since.IsZero() {
		steady.since = now
	}
	return now.Sub(steady.since) >= period
}

// ownUpdate tells which update of a service is nemo's: none when nemo left the service as it was,
// else any that Swarm started after the one it showed before nemo's change. Both times are the
// manager's: the installer's own clock may not agree with it.
type ownUpdate struct {
	changed bool
	after   time.Time
}

func (own ownUpdate) of(status *swarm.UpdateStatus) bool {
	return own.changed && status != nil && status.StartedAt != nil && status.StartedAt.After(own.after)
}

// verdict is what one look at a service says of nemo's change.
type verdict int

const (
	// progressing: Swarm is still bringing the tasks up.
	progressing verdict = iota
	// converged: the tasks of the service's spec run, or completed for a job.
	converged
	// rolledBack: Swarm rolled nemo's update back.
	rolledBack
	// gaveUp: Swarm stopped restarting a failed task, as its restart policy says.
	gaveUp
)

// observation is one look at a service: its verdict, and what to show of it.
type observation struct {
	verdict verdict
	// rollback is Swarm's message when it rolled nemo's update back.
	rollback string

	running, desired uint64
	phase            string
}

// observe looks at the service and its up-to-date tasks. The phase shown is the state of the least
// advanced task, why the last one failed while Swarm restarts it, or verifying once converged.
func observe(service swarm.Service, tasks []swarm.Task, own ownUpdate) observation {
	look := observation{}
	look.verdict, look.rollback = verdictOf(service, tasks, own)
	look.running, look.desired, look.phase = tasksOf(service, tasks)
	switch reason := lastError(tasks); {
	case look.verdict == converged:
		look.phase = "verifying"
	case reason != "":
		look.phase = "restarting: " + reason
	}
	return look
}

// verdictOf says where nemo's change stands, and Swarm's message when it rolled it back. A task
// with a healthcheck only runs once healthy, so depends_on conditions on health hold too. An update
// Swarm paused as a task failed is not a failure: the restart policy restarts that task until it runs.
func verdictOf(service swarm.Service, tasks []swarm.Task, own ownUpdate) (verdict, string) {
	if status := service.UpdateStatus; own.of(status) {
		switch status.State {
		case swarm.UpdateStateUpdating:
			return progressing, ""
		case swarm.UpdateStateRollbackStarted, swarm.UpdateStateRollbackPaused, swarm.UpdateStateRollbackCompleted:
			return rolledBack, status.Message
		}
	}
	switch {
	case gaveUpOnAReplica(service.Spec.Mode, tasks):
		return gaveUp, ""
	case tasksConverged(service, tasks):
		return converged, ""
	}
	return progressing, ""
}

// replica is what Swarm keeps running: a slot of a replicated service, a node of a global one.
// Swarm restarts a failed task in a new task of its replica, created without a node until it is
// scheduled for a replicated service, and without a slot for a global one.
type replica struct {
	slot int
	node string
}

func replicaOf(mode swarm.ServiceMode, task swarm.Task) replica {
	if mode.Global != nil || mode.GlobalJob != nil {
		return replica{node: task.NodeID}
	}
	return replica{slot: task.Slot}
}

// gaveUpOnAReplica tells whether Swarm stopped restarting a replica: one of its tasks failed, and
// Swarm means to run none of them anymore.
func gaveUpOnAReplica(mode swarm.ServiceMode, tasks []swarm.Task) bool {
	byReplica := map[replica][]swarm.Task{}
	for _, task := range tasks {
		byReplica[replicaOf(mode, task)] = append(byReplica[replicaOf(mode, task)], task)
	}
	for _, tasks := range byReplica {
		if slices.ContainsFunc(tasks, failed) && !slices.ContainsFunc(tasks, wanted) {
			return true
		}
	}
	return false
}

// tasksConverged tells whether the tasks of the service's spec run, or completed for a job.
func tasksConverged(service swarm.Service, tasks []swarm.Task) bool {
	switch mode := service.Spec.Mode; {
	case mode.ReplicatedJob != nil:
		total := uint64(1)
		if mode.ReplicatedJob.TotalCompletions != nil {
			total = *mode.ReplicatedJob.TotalCompletions
		}
		return count(tasks, completed) >= total
	case mode.GlobalJob != nil:
		return count(tasks, completed) > 0 && !slices.ContainsFunc(tasks, active)
	}
	status := service.ServiceStatus
	return status != nil && status.RunningTasks == status.DesiredTasks && count(tasks, runningAsWanted) == status.DesiredTasks
}

// lastError is why the last failed task failed, or nothing when none did.
func lastError(tasks []swarm.Task) string {
	var last *swarm.Task
	for i, task := range tasks {
		if failed(task) && (last == nil || task.CreatedAt.After(last.CreatedAt)) {
			last = &tasks[i]
		}
	}
	if last == nil {
		return ""
	}
	return cmp.Or(last.Status.Err, string(last.Status.State))
}

func failed(task swarm.Task) bool {
	return task.Status.State == swarm.TaskStateFailed || task.Status.State == swarm.TaskStateRejected
}

// wanted tells whether Swarm still means to run the task.
func wanted(task swarm.Task) bool {
	return !slices.Contains([]swarm.TaskState{swarm.TaskStateShutdown, swarm.TaskStateRemove, swarm.TaskStateOrphaned}, task.DesiredState)
}

func completed(task swarm.Task) bool { return task.Status.State == swarm.TaskStateComplete }

func runningAsWanted(task swarm.Task) bool {
	return task.Status.State == swarm.TaskStateRunning && task.DesiredState == swarm.TaskStateRunning
}

// active tells whether the task has not ended yet.
func active(task swarm.Task) bool {
	return !slices.Contains([]swarm.TaskState{
		swarm.TaskStateComplete, swarm.TaskStateShutdown, swarm.TaskStateFailed,
		swarm.TaskStateRejected, swarm.TaskStateRemove, swarm.TaskStateOrphaned,
	}, task.Status.State)
}

func count(tasks []swarm.Task, matches func(swarm.Task) bool) uint64 {
	var n uint64
	for _, task := range tasks {
		if matches(task) {
			n++
		}
	}
	return n
}

// tasksOf counts the running and desired tasks of the service, and names the
// state of its least advanced up-to-date task: preparing, starting, running.
func tasksOf(service swarm.Service, tasks []swarm.Task) (running, desired uint64, phase string) {
	if status := service.ServiceStatus; status != nil {
		running, desired = status.RunningTasks, status.DesiredTasks
	}
	phase = string(swarm.TaskStateRunning)
	least := len(order)
	for _, task := range tasks {
		if task.DesiredState != swarm.TaskStateRunning {
			continue
		}
		if index := slices.Index(order, task.Status.State); index >= 0 && index < least {
			least, phase = index, string(task.Status.State)
		}
	}
	return running, desired, phase
}

// order is the states of a task on its way to running, the earliest first.
var order = []swarm.TaskState{
	swarm.TaskStateNew, swarm.TaskStatePending, swarm.TaskStateAssigned, swarm.TaskStateAccepted,
	swarm.TaskStatePreparing, swarm.TaskStateReady, swarm.TaskStateStarting, swarm.TaskStateRunning,
}

func monitor(service swarm.Service) time.Duration {
	switch {
	case service.Spec.Mode.ReplicatedJob != nil || service.Spec.Mode.GlobalJob != nil:
		return 0
	case service.Spec.UpdateConfig != nil && service.Spec.UpdateConfig.Monitor != 0:
		return service.Spec.UpdateConfig.Monitor
	}
	return 5 * time.Second
}

// serviceState reads the service with its task counts, and its up-to-date tasks: "_up-to-date" is the
// filter docker service update uses to leave out the tasks of the previous spec.
func (orchestrator *Orchestrator) serviceState(ctx context.Context, id string) (swarm.Service, []swarm.Task, error) {
	services, err := orchestrator.docker.ServiceList(ctx, client.Filters{}.Add("id", id))
	if err != nil {
		return swarm.Service{}, nil, err
	}
	i := slices.IndexFunc(services, func(service swarm.Service) bool { return service.ID == id })
	if i < 0 {
		return swarm.Service{}, nil, fmt.Errorf("service %s disappeared", id)
	}
	tasks, err := orchestrator.docker.TaskList(ctx, client.Filters{}.Add("service", id).Add("_up-to-date", "true"))
	if err != nil {
		return swarm.Service{}, nil, err
	}
	return services[i], tasks, nil
}

// withTaskError adds the last error of the service's tasks, which usually says why it failed.
func (orchestrator *Orchestrator) withTaskError(ctx context.Context, id string, err error) error {
	tasks, listErr := orchestrator.docker.TaskList(ctx, client.Filters{}.Add("service", id))
	if listErr != nil {
		return err
	}
	slices.SortFunc(tasks, func(left, right swarm.Task) int { return right.Status.Timestamp.Compare(left.Status.Timestamp) })
	for _, task := range tasks {
		if task.Status.Err != "" {
			return fmt.Errorf("%w: %s", err, task.Status.Err)
		}
	}
	return err
}
