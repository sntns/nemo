package docker

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
)

// Client drives the Docker Engine of the target. Its methods take the specs
// the orchestrators convert the compose project into, find objects by their
// exact name, and name the object in their errors.
type Client struct {
	api client.APIClient
}

// API is the Engine API itself, for the clients built on this one.
func (docker *Client) API() client.APIClient { return docker.api }

// New returns the client of an Engine API connection: the runtime's, which
// reaches the target the way the docker CLI does.
func New(api client.APIClient) *Client {
	return &Client{api: api}
}

// Connect connects to the Docker Engine of the environment: the socket the
// runtime mounted, with the io.cnab.docker extension, or DOCKER_HOST and its
// TLS settings when the installer runs away from the target.
func Connect(ctx context.Context) (*Client, error) {
	api, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("connecting to Docker: %w", err)
	}
	if _, err := api.Ping(ctx, client.PingOptions{}); err != nil {
		api.Close()
		return nil, fmt.Errorf("reaching Docker: mount its socket or set DOCKER_HOST: %w", err)
	}
	return &Client{api: api}, nil
}

func (docker *Client) Close() error { return docker.api.Close() }

// Info describes the daemon.
func (docker *Client) Info(ctx context.Context) (system.Info, error) {
	result, err := docker.api.Info(ctx, client.InfoOptions{})
	if err != nil {
		return system.Info{}, fmt.Errorf("reaching Docker: %w", err)
	}
	return result.Info, nil
}
