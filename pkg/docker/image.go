package docker

import (
	"context"
	"fmt"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
)

// ImageInspect describes an image the daemon has.
func (docker *Client) ImageInspect(ctx context.Context, image string) (image.InspectResponse, error) {
	result, err := docker.api.ImageInspect(ctx, image)
	if err != nil {
		return result.InspectResponse, fmt.Errorf("inspecting image %s: %w", image, err)
	}
	return result.InspectResponse, nil
}

// ImageEnsure pulls the image, with the encoded credentials of its
// registry, unless it is on the daemon and the pull policy lets it be used
// as is.
func (docker *Client) ImageEnsure(ctx context.Context, image, policy, registryAuth string) error {
	switch policy {
	case "", types.PullPolicyMissing, types.PullPolicyIfNotPresent, types.PullPolicyNever:
		_, err := docker.api.ImageInspect(ctx, image)
		switch {
		case err == nil:
			return nil
		case !IsNotFound(err):
			return fmt.Errorf("inspecting image %s: %w", image, err)
		case policy == types.PullPolicyNever:
			return fmt.Errorf("image %s is not on the daemon, and pull_policy is never", image)
		}
	}
	response, err := docker.api.ImagePull(ctx, image, client.ImagePullOptions{RegistryAuth: registryAuth})
	if err != nil {
		return fmt.Errorf("pulling image %s: %w", image, err)
	}
	if err = response.Wait(ctx); err != nil {
		return fmt.Errorf("pulling image %s: %w", image, err)
	}
	return nil
}
