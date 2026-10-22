package swarm

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"

	"github.com/sntns/nemo/pkg/docker"
)

func (swarmClient *Client) SecretCreate(ctx context.Context, spec *swarm.SecretSpec) (string, error) {
	created, err := swarmClient.API().SecretCreate(ctx, client.SecretCreateOptions{Spec: *spec})
	if err != nil {
		return "", fmt.Errorf("creating secret %s: %w", spec.Name, err)
	}
	return created.ID, nil
}

// SecretFind returns the secret of that exact name, or nil.
func (swarmClient *Client) SecretFind(ctx context.Context, name string) (*swarm.Secret, error) {
	secrets, err := swarmClient.SecretList(ctx, docker.ByName(name))
	if err != nil {
		return nil, err
	}
	for _, secret := range secrets {
		if secret.Spec.Name == name {
			return &secret, nil
		}
	}
	return nil, nil
}

func (swarmClient *Client) SecretList(ctx context.Context, filters client.Filters) ([]swarm.Secret, error) {
	list, err := swarmClient.API().SecretList(ctx, client.SecretListOptions{Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("listing secrets: %w", err)
	}
	return list.Items, nil
}

// SecretUpdate replaces the labels of an existing secret: Swarm changes
// nothing else of a secret, its data least of all.
func (swarmClient *Client) SecretUpdate(ctx context.Context, existing *swarm.Secret, spec *swarm.SecretSpec) error {
	options := client.SecretUpdateOptions{Version: existing.Version, Spec: *spec}
	if _, err := swarmClient.API().SecretUpdate(ctx, existing.ID, options); err != nil {
		return fmt.Errorf("updating secret %s: %w", spec.Name, err)
	}
	return nil
}

// SecretDelete removes the secret. The error tells, with docker.IsInUse, that a
// service still uses it.
func (swarmClient *Client) SecretDelete(ctx context.Context, secret *swarm.Secret) error {
	if _, err := swarmClient.API().SecretRemove(ctx, secret.ID, client.SecretRemoveOptions{}); err != nil && !docker.IsNotFound(err) {
		return fmt.Errorf("removing secret %s: %w", secret.Spec.Name, err)
	}
	return nil
}
