package swarm

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/swarm"

	"github.com/sntns/nemo/pkg/docker"
)

// Client is the Engine client of the Swarm orchestrator: the one every
// Docker has, and what only a swarm has, services, tasks, configs and
// secrets, one file per kind.
type Client struct {
	*docker.Client
}

// connect connects to the Docker of the target, which must be a manager of
// an active swarm.
func connect(ctx context.Context) (*Client, error) {
	shared, err := docker.Connect(ctx)
	if err != nil {
		return nil, err
	}
	info, err := shared.Info(ctx)
	if err != nil {
		shared.Close()
		return nil, err
	}
	if state := info.Swarm; state.LocalNodeState != swarm.LocalNodeStateActive || !state.ControlAvailable {
		shared.Close()
		return nil, fmt.Errorf("this Docker is not a swarm manager (swarm %s): target a manager, or the compose orchestrator", state.LocalNodeState)
	}
	return &Client{Client: shared}, nil
}
