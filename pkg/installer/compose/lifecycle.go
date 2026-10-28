package compose

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/sntns/nemo/pkg/installer/compose/convert"
)

// steadyFor is how long a container must stay up, without the daemon restarting it, before its
// service counts as up: as with Swarm's monitor period, a container that starts then exits is caught.
const steadyFor = 5 * time.Second

// wait waits until the container reaches the wanted condition, and stays there for steadyFor
// unless it was to complete.
func (orchestrator *Orchestrator) wait(ctx context.Context, id string, wanted convert.Condition) error {
	var steady steadiness
	for {
		inspected, err := orchestrator.docker.ContainerInspect(ctx, id)
		if err != nil {
			return err
		}
		done, err := reached(inspected.State, wanted)
		switch {
		case err != nil:
			return err
		case !done:
			steady = steadiness{}
		case wanted == convert.Completed || steady.held(inspected.State, time.Now()):
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("not %s in time%s: %w", wanted, lastExit(inspected.State), ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// steadiness follows a container that reached its condition: it holds once the container ran
// for steadyFor without starting again.
type steadiness struct {
	startedAt string
	since     time.Time
}

func (steady *steadiness) held(state *container.State, now time.Time) bool {
	if state.StartedAt != steady.startedAt {
		*steady = steadiness{startedAt: state.StartedAt, since: now}
	}
	return now.Sub(steady.since) >= steadyFor
}

// lastExit says how the container last exited, when it did.
func lastExit(state *container.State) string {
	if state == nil || state.ExitCode == 0 {
		return ""
	}
	return fmt.Sprintf(", it last exited with code %d", state.ExitCode)
}

// waiting is what a service does until its containers reach the condition.
func waiting(condition convert.Condition) string {
	switch condition {
	case convert.Healthy:
		return "checking health"
	case convert.Completed:
		return "running to completion"
	}
	return "starting"
}

// reached tells whether a container is in the wanted state, and fails when it never will be.
func reached(state *container.State, wanted convert.Condition) (bool, error) {
	switch {
	case state == nil:
		return false, nil
	case state.Running || state.Restarting:
		if wanted == convert.Completed || state.Restarting {
			return false, nil
		}
		if wanted == convert.Started || state.Health == nil {
			return true, nil
		}
		switch state.Health.Status {
		case container.Healthy, container.NoHealthcheck:
			return true, nil
		case container.Unhealthy:
			var output string
			if log := state.Health.Log; len(log) > 0 {
				output = strings.TrimSpace(log[len(log)-1].Output)
			}
			return false, fmt.Errorf("unhealthy%s", prefixed(output))
		}
		return false, nil
	case state.Status == container.StateExited && state.ExitCode == 0 && wanted == convert.Completed:
		return true, nil
	case state.Status == container.StateExited || state.Status == container.StateDead:
		return false, fmt.Errorf("exited with code %d%s", state.ExitCode, prefixed(state.Error))
	}
	return false, nil
}

func prefixed(text string) string {
	if text == "" {
		return ""
	}
	return ": " + text
}
