package main

import (
	"context"
	"fmt"
	"io"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"

	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/target"
)

// ensureSwarm makes the Docker of this machine a swarm manager when the
// target deploys there with swarm and it is in no swarm: a target
// elsewhere is left as it is.
func ensureSwarm(ctx context.Context, deploymentTarget target.Target, output io.Writer) error {
	if deploymentTarget.Orchestrator != target.Swarm || deploymentTarget.Host != "" || deploymentTarget.Context != "" {
		return nil
	}
	engine, err := docker.Connect(ctx)
	if err != nil {
		return err
	}
	defer engine.Close()
	info, err := engine.Info(ctx)
	if err != nil || info.Swarm.LocalNodeState != swarm.LocalNodeStateInactive {
		return err
	}
	if _, err = engine.API().SwarmInit(ctx, client.SwarmInitOptions{ListenAddr: "0.0.0.0:2377"}); err != nil {
		return fmt.Errorf("creating a swarm on this machine's Docker: %w", err)
	}
	fmt.Fprintln(output, "Created a swarm on this machine's Docker")
	return nil
}
