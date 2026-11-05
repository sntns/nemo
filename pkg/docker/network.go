package docker

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// NetworkSpec is a network to create: its name and its options, as both
// orchestrators convert a compose network.
type NetworkSpec struct {
	Name    string
	Options client.NetworkCreateOptions
}

func (docker *Client) NetworkCreate(ctx context.Context, spec NetworkSpec) error {
	if _, err := docker.api.NetworkCreate(ctx, spec.Name, spec.Options); err != nil {
		return fmt.Errorf("creating network %s: %w", spec.Name, err)
	}
	return nil
}

// NetworkFind returns the network of that exact name, or nil.
func (docker *Client) NetworkFind(ctx context.Context, name string) (*network.Summary, error) {
	// The daemon matches the name on its parts: my_network lists my_network_private too.
	list, err := docker.api.NetworkList(ctx, client.NetworkListOptions{Filters: ByName(name)})
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}
	index := slices.IndexFunc(list.Items, func(summary network.Summary) bool { return summary.Name == name })
	if index < 0 {
		return nil, nil
	}
	return &list.Items[index], nil
}

func (docker *Client) NetworkDelete(ctx context.Context, network *network.Summary) error {
	if _, err := docker.api.NetworkRemove(ctx, network.ID, client.NetworkRemoveOptions{}); err != nil && !IsNotFound(err) {
		return fmt.Errorf("removing network %s: %w", network.Name, err)
	}
	return nil
}

// NetworkList returns the networks the filters select.
func (docker *Client) NetworkList(ctx context.Context, filters client.Filters) ([]network.Summary, error) {
	list, err := docker.api.NetworkList(ctx, client.NetworkListOptions{Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}
	return list.Items, nil
}

// How long, and how often, the removal of a network nothing uses any more is
// tried again: Swarm releases a network a moment after the containers of its
// services are gone.
var (
	networkRelease = 30 * time.Second
	networkRetry   = time.Second
)

// NetworkRemove removes the network. When the daemon answers that it is in
// use while used finds nothing that uses it, the network is about to be
// released: its removal is tried again, for a while. The error is then the
// daemon's, which IsInUse tells.
func (docker *Client) NetworkRemove(ctx context.Context, existing *network.Summary, used func(context.Context) (bool, error)) error {
	deadline := time.Now().Add(networkRelease)
	for {
		err := docker.NetworkDelete(ctx, existing)
		if err == nil || !IsInUse(err) || time.Now().After(deadline) {
			return err
		}
		if inUse, usedErr := used(ctx); inUse || usedErr != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(networkRetry):
		}
	}
}
