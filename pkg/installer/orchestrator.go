package installer

import (
	"context"
	"errors"

	"github.com/sntns/nemo/pkg/progress"
)

// ErrUnsupported is returned for an action or an orchestrator the installer does not handle.
var ErrUnsupported = errors.New("unsupported")

// Orchestrator applies a compose project to the target: Compose on a single
// Docker host, or Swarm. It knows nothing about CNAB.
type Orchestrator interface {
	// Name is the value of the nemo-orchestrator parameter that selects it.
	Name() string

	// Validate reports what it can't honour in the project, without touching the target.
	Validate(project *Project) error

	// Install brings the installation to the project: it creates what is
	// missing, updates what changed, removes what the project no longer
	// declares, and waits until everything converged. It is also the upgrade.
	Install(ctx context.Context, project *Project, opts InstallOptions) error

	// Uninstall removes everything the installation created.
	Uninstall(ctx context.Context, project *Project, opts UninstallOptions) error

	// Close releases the connection to the target.
	Close() error
}

// NewOrchestratorFunc connects an orchestrator to the target.
type NewOrchestratorFunc func(ctx context.Context) (Orchestrator, error)

// Orchestrators are the orchestrators an installer knows, by the value of
// the nemo-orchestrator parameter. Only the one named is built.
type Orchestrators map[string]NewOrchestratorFunc

// InstallOptions are the options of Orchestrator.Install.
type InstallOptions struct {
	Events progress.Sink
}

// UninstallOptions are the options of Orchestrator.Uninstall.
type UninstallOptions struct {
	Events progress.Sink
	// RemoveVolumes also removes the volumes, and the data they hold.
	RemoveVolumes bool
}
