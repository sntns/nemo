package compose

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/sntns/nemo/pkg/installer/compose/convert"
)

func (orchestrator *Orchestrator) wait(ctx context.Context, id string, wanted convert.Condition) error {
	for {
		inspected, err := orchestrator.docker.ContainerInspect(ctx, id)
		if err != nil {
			return err
		}
		done, err := reached(inspected.State, wanted)
		if done || err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("not %s in time: %w", wanted, ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
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
