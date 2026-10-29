// Command gill is the entrypoint of nemo's invocation image,
// /cnab/app/run: it performs the CNAB action the runtime hands to the
// container.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/installer/compose"
	"github.com/sntns/nemo/pkg/installer/swarm"
	"github.com/sntns/nemo/pkg/progress"
)

// orchestrators are the orchestrators this installer image knows. Run
// builds only the one the nemo-orchestrator parameter names.
var orchestrators = installer.Orchestrators{
	compose.Name: compose.New,
	swarm.Name:   swarm.New,
}

func main() {
	events, failure := progress.Installer(os.Getenv(progress.Variable), os.Stdout, os.Stderr)
	if err := run(events); err != nil {
		fmt.Fprintln(failure, "error:", err)
		os.Exit(1)
	}
}

func run(events progress.Sink) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	operation, err := installer.ReadOperation(installer.Container{LookupEnv: os.LookupEnv, FS: os.DirFS("/")})
	if err != nil {
		return err
	}
	return installer.Run(ctx, operation, orchestrators, events)
}
