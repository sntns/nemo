package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

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
	if err = initSwarm(ctx, engine.API()); err != nil {
		return fmt.Errorf("creating a swarm on this machine's Docker: %w", err)
	}
	fmt.Fprintln(output, "Created a swarm on this machine's Docker")
	return nil
}

// initSwarm creates a swarm of this Docker. On a machine with several
// network interfaces, Docker asks which address to advertise: it is the one
// of the default route, as 127.0.0.1 would break the published ports.
func initSwarm(ctx context.Context, api client.APIClient) error {
	options := client.SwarmInitOptions{ListenAddr: "0.0.0.0:2377"}
	_, err := api.SwarmInit(ctx, options)
	if err == nil || !strings.Contains(err.Error(), "advertise") {
		return err
	}
	// No packet is sent: dialing UDP only picks the route and its address.
	connection, dialErr := net.Dial("udp", "1.1.1.1:53")
	if dialErr != nil {
		return errors.Join(err, dialErr)
	}
	defer connection.Close()
	options.AdvertiseAddr = connection.LocalAddr().(*net.UDPAddr).IP.String()
	_, err = api.SwarmInit(ctx, options)
	return err
}
