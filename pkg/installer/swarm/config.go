package swarm

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"

	"github.com/sntns/nemo/pkg/docker"
)

func (swarmClient *Client) ConfigCreate(ctx context.Context, spec *swarm.ConfigSpec) (string, error) {
	created, err := swarmClient.API().ConfigCreate(ctx, client.ConfigCreateOptions{Spec: *spec})
	if err != nil {
		return "", fmt.Errorf("creating config %s: %w", spec.Name, err)
	}
	return created.ID, nil
}

// ConfigFind returns the config of that exact name, or nil.
func (swarmClient *Client) ConfigFind(ctx context.Context, name string) (*swarm.Config, error) {
	configs, err := swarmClient.ConfigList(ctx, docker.ByName(name))
	if err != nil {
		return nil, err
	}
	for _, config := range configs {
		if config.Spec.Name == name {
			return &config, nil
		}
	}
	return nil, nil
}

func (swarmClient *Client) ConfigList(ctx context.Context, filters client.Filters) ([]swarm.Config, error) {
	list, err := swarmClient.API().ConfigList(ctx, client.ConfigListOptions{Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("listing configs: %w", err)
	}
	return list.Items, nil
}

// ConfigUpdate replaces the labels of an existing config: Swarm changes
// nothing else of a config, its data least of all.
func (swarmClient *Client) ConfigUpdate(ctx context.Context, existing *swarm.Config, spec *swarm.ConfigSpec) error {
	options := client.ConfigUpdateOptions{Version: existing.Version, Spec: *spec}
	if _, err := swarmClient.API().ConfigUpdate(ctx, existing.ID, options); err != nil {
		return fmt.Errorf("updating config %s: %w", spec.Name, err)
	}
	return nil
}

// ConfigDelete removes the config. The error tells, with docker.IsInUse, that a
// service still uses it.
func (swarmClient *Client) ConfigDelete(ctx context.Context, config *swarm.Config) error {
	if _, err := swarmClient.API().ConfigRemove(ctx, config.ID, client.ConfigRemoveOptions{}); err != nil && !docker.IsNotFound(err) {
		return fmt.Errorf("removing config %s: %w", config.Spec.Name, err)
	}
	return nil
}
