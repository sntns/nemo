// Package installer is the invocation image side of nemo: it reads the
// operation a CNAB runtime hands to the container and applies the bundle's
// compose project to the target with the Orchestrator the target names. It
// keeps no state and knows nothing about stacks, storage or the nemo runtime.
package installer

import (
	"context"
	"fmt"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/progress"
)

// Run performs the operation with the orchestrator the nemo-orchestrator
// parameter names. Only that one is built.
func Run(ctx context.Context, operation Operation, orchestrators Orchestrators, events progress.Sink) error {
	project, err := LoadProject(ctx, operation)
	if err != nil {
		return err
	}

	opts, err := optionsOf(operation.Parameters)
	if err != nil {
		return err
	}
	if opts.Orchestrator == "" {
		return fmt.Errorf("parameter %s is not set: the target says compose or swarm, the installer does not guess it", cnab.ParameterOrchestrator)
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	orchestratorFunc, ok := orchestrators[opts.Orchestrator]
	if !ok {
		return fmt.Errorf("orchestrator %s: %w", opts.Orchestrator, ErrUnsupported)
	}
	orchestrator, err := orchestratorFunc(ctx)
	if err != nil {
		return fmt.Errorf("orchestrator %s: %w", opts.Orchestrator, err)
	}
	defer orchestrator.Close()
	events.Emit(progress.Event{Group: "orchestrator", Item: orchestrator.Name(), State: progress.Done})

	switch operation.Action {
	case cnab.ActionInstall, cnab.ActionUpgrade:
		if err = orchestrator.Validate(project); err != nil {
			return err
		}
		opts := InstallOptions{
			Events: events,
		}
		return orchestrator.Install(ctx, project, opts)
	case cnab.ActionValidate:
		return orchestrator.Validate(project)
	case cnab.ActionUninstall:
		opts := UninstallOptions{
			Events:        events,
			RemoveVolumes: opts.Purge,
		}
		return orchestrator.Uninstall(ctx, project, opts)
	default:
		return fmt.Errorf("action %s: %w", operation.Action, ErrUnsupported)
	}
}
