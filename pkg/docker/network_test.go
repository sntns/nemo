package docker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// releasing is a Docker whose network is in use the first times it is removed.
type releasing struct {
	client.APIClient
	inUse   int
	removed int
}

func (engine *releasing) NetworkRemove(ctx context.Context, id string, options client.NetworkRemoveOptions) (client.NetworkRemoveResult, error) {
	engine.removed++
	if engine.removed <= engine.inUse {
		return client.NetworkRemoveResult{}, errors.New("error while removing network: network demo_mesh id 1 has active endpoints")
	}
	return client.NetworkRemoveResult{}, nil
}

func TestNetworkRemove(t *testing.T) {
	networkRetry = time.Millisecond
	mesh := &network.Summary{}
	mesh.ID, mesh.Name = "1", "demo_mesh"
	unused := func(context.Context) (bool, error) { return false, nil }

	engine := &releasing{inUse: 2}
	require.NoError(t, New(engine).NetworkRemove(context.Background(), mesh, unused), "removed once released")
	assert.Equal(t, 3, engine.removed)

	engine = &releasing{inUse: 2}
	err := New(engine).NetworkRemove(context.Background(), mesh, func(context.Context) (bool, error) { return true, nil })
	assert.True(t, IsInUse(err), "a network something uses is not waited for")
	assert.Equal(t, 1, engine.removed)
}
