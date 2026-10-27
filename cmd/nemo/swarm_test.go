package main

import (
	"context"
	"errors"
	"testing"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// multihomed is a Docker on a machine with several network interfaces: it
// creates a swarm only once told which address to advertise.
type multihomed struct {
	client.APIClient
	advertised []string
}

func (docker *multihomed) SwarmInit(ctx context.Context, options client.SwarmInitOptions) (client.SwarmInitResult, error) {
	docker.advertised = append(docker.advertised, options.AdvertiseAddr)
	if options.AdvertiseAddr == "" {
		return client.SwarmInitResult{}, errors.New("could not choose an IP address to advertise since this system has multiple addresses on different interfaces")
	}
	return client.SwarmInitResult{}, nil
}

func TestInitSwarm_severalInterfaces(t *testing.T) {
	t.Parallel()
	docker := &multihomed{}
	require.NoError(t, initSwarm(context.Background(), docker))
	require.Len(t, docker.advertised, 2)
	assert.Empty(t, docker.advertised[0], "Docker chooses the address when it can")
	assert.NotEmpty(t, docker.advertised[1], "else the address of the default route")
	assert.NotEqual(t, "127.0.0.1", docker.advertised[1])
}
