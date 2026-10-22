package swarm

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"

	"github.com/sntns/nemo/pkg/docker"
)

// ServiceCreate creates the service. Swarm hands the encoded registry
// credentials to the nodes that pull its image.
func (swarmClient *Client) ServiceCreate(ctx context.Context, spec *swarm.ServiceSpec, registryAuth string) (string, error) {
	created, err := swarmClient.API().ServiceCreate(ctx, client.ServiceCreateOptions{Spec: *spec, EncodedRegistryAuth: registryAuth})
	if err != nil {
		return "", fmt.Errorf("creating service %s: %w", spec.Name, err)
	}
	return created.ID, nil
}

// ServiceFind returns the service of that exact name, or nil.
func (swarmClient *Client) ServiceFind(ctx context.Context, name string) (*swarm.Service, error) {
	services, err := swarmClient.ServiceList(ctx, docker.ByName(name))
	if err != nil {
		return nil, err
	}
	for _, service := range services {
		if service.Spec.Name == name {
			return &service, nil
		}
	}
	return nil, nil
}

// ServiceList lists services, with their task counts.
func (swarmClient *Client) ServiceList(ctx context.Context, filters client.Filters) ([]swarm.Service, error) {
	list, err := swarmClient.API().ServiceList(ctx, client.ServiceListOptions{Filters: filters, Status: true})
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	return list.Items, nil
}

// ServiceUpdate replaces the spec of an existing service, with the
// encoded registry credentials of its image.
func (swarmClient *Client) ServiceUpdate(ctx context.Context, existing *swarm.Service, spec *swarm.ServiceSpec, registryAuth string) error {
	if _, err := swarmClient.API().ServiceUpdate(ctx, existing.ID, client.ServiceUpdateOptions{Version: existing.Version, Spec: *spec, EncodedRegistryAuth: registryAuth}); err != nil {
		return fmt.Errorf("updating service %s: %w", spec.Name, err)
	}
	return nil
}

// ServiceDelete removes the service. Swarm removes the containers of its
// tasks afterwards.
func (swarmClient *Client) ServiceDelete(ctx context.Context, service *swarm.Service) error {
	if _, err := swarmClient.API().ServiceRemove(ctx, service.ID, client.ServiceRemoveOptions{}); err != nil && !docker.IsNotFound(err) {
		return fmt.Errorf("removing service %s: %w", service.Spec.Name, err)
	}
	return nil
}

func (swarmClient *Client) TaskList(ctx context.Context, filters client.Filters) ([]swarm.Task, error) {
	list, err := swarmClient.API().TaskList(ctx, client.TaskListOptions{Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	return list.Items, nil
}
