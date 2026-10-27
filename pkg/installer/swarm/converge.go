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

// converge waits until the service has converged and stays so for its update monitor period, the
// way docker service update does: a task that starts then crashes is caught. It reports its
// running and desired tasks and the state of the least advanced, when they change.
func (orchestrator *Orchestrator) converge(ctx context.Context, id string, since time.Time, report func(running, desired uint64, phase string)) error {
	var stable time.Time
	var reported string
	for {
		service, tasks, err := orchestrator.serviceState(ctx, id)
		if err != nil {
			return err
		}
		done, err := convergence(service, tasks, since)
		if err != nil {
			return orchestrator.withTaskError(ctx, id, err)
		}
		switch {
		case !done:
			stable = time.Time{}
		case stable.IsZero():
			stable = time.Now()
		}
		if done && time.Since(stable) >= monitor(service) {
			return nil
		}
		// Converged, the tasks must still stay up for the monitor period, as docker's verify.
		running, desired, phase := tasksOf(service, tasks)
		if reason, _ := lastFailure(tasks); reason != "" {
			phase = "restarting: " + reason
		}
		if done {
			phase = "verifying"
		}
		if now := fmt.Sprint(running, desired, phase); now != reported {
			report(running, desired, phase)
			reported = now
		}
		select {
		case <-ctx.Done():
			return orchestrator.withTaskError(context.WithoutCancel(ctx), id, fmt.Errorf("not converged in time: %w", ctx.Err()))
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// convergence tells whether a service has converged: the update started after since went through,
// and the up-to-date tasks run, or completed for a job. A task with a healthcheck only runs once
// healthy, so depends_on conditions on health hold too. It fails when Swarm rolled back that update,
// or stopped restarting a failed task as its restart policy says. One Swarm paused as a task failed
// is not: the restart policy restarts that task until it runs.
func convergence(service swarm.Service, tasks []swarm.Task, since time.Time) (bool, error) {
	if status := service.UpdateStatus; status != nil && status.StartedAt != nil && !status.StartedAt.Before(since) {
		switch status.State {
		case swarm.UpdateStateUpdating:
			return false, nil
		case swarm.UpdateStateRollbackStarted, swarm.UpdateStateRollbackPaused, swarm.UpdateStateRollbackCompleted:
			return false, fmt.Errorf("swarm rolled the update back: %s", status.Message)
		}
	}
	if _, gaveUp := lastFailure(tasks); gaveUp {
		return false, errors.New("swarm stopped restarting its task, as its restart policy says")
	}
	var running, completed, active uint64
	for _, task := range tasks {
		switch task.Status.State {
		case swarm.TaskStateRunning:
			if task.DesiredState == swarm.TaskStateRunning {
				running++
			}
		case swarm.TaskStateComplete:
			completed++
		}
		if !slices.Contains(terminal, task.Status.State) {
			active++
		}
	}
	switch mode := service.Spec.Mode; {
	case mode.ReplicatedJob != nil:
		total := uint64(1)
		if mode.ReplicatedJob.TotalCompletions != nil {
			total = *mode.ReplicatedJob.TotalCompletions
		}
		return completed >= total, nil
	case mode.GlobalJob != nil:
		return completed > 0 && active == 0, nil
	}
	status := service.ServiceStatus
	return status != nil && running == status.DesiredTasks && status.RunningTasks == status.DesiredTasks, nil
}

// lastFailure returns the error of the last task that failed, and whether Swarm gave up on a failed
// task: it restarts one in a new task of the same slot, or of the same node for a global service,
// and creates none once its restart policy says to stop.
func lastFailure(tasks []swarm.Task) (reason string, gaveUp bool) {
	var last *swarm.Task
	for i, task := range tasks {
		if !slices.Contains(failed, task.Status.State) {
			continue
		}
		if last == nil || task.CreatedAt.After(last.CreatedAt) {
			last = &tasks[i]
		}
		restarted := slices.ContainsFunc(tasks, func(other swarm.Task) bool {
			return other.Slot == task.Slot && other.NodeID == task.NodeID && !slices.Contains(stopped, other.DesiredState)
		})
		gaveUp = gaveUp || !restarted
	}
	if last == nil {
		return "", false
	}
	return cmp.Or(last.Status.Err, string(last.Status.State)), gaveUp
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

var failed = []swarm.TaskState{swarm.TaskStateFailed, swarm.TaskStateRejected}

// stopped is the desired states of a task Swarm will not run anymore.
var stopped = []swarm.TaskState{swarm.TaskStateShutdown, swarm.TaskStateRemove, swarm.TaskStateOrphaned}

var terminal = []swarm.TaskState{
	swarm.TaskStateComplete, swarm.TaskStateShutdown, swarm.TaskStateFailed,
	swarm.TaskStateRejected, swarm.TaskStateRemove, swarm.TaskStateOrphaned,
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
