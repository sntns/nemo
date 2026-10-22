package docker

import (
	"context"
	"fmt"
	"slices"

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
