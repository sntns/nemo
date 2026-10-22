package docker

import (
	"context"
	"fmt"
	"io"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func (docker *Client) ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (string, error) {
	created, err := docker.api.ContainerCreate(ctx, options)
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", options.Name, err)
	}
	return created.ID, nil
}

// ContainerList lists containers, stopped ones included.
func (docker *Client) ContainerList(ctx context.Context, filters client.Filters) ([]container.Summary, error) {
	list, err := docker.api.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	return list.Items, nil
}

func (docker *Client) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	result, err := docker.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return container.InspectResponse{}, fmt.Errorf("inspecting %s: %w", id[:12], err)
	}
	return result.Container, nil
}

func (docker *Client) ContainerStart(ctx context.Context, id string) error {
	if _, err := docker.api.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("starting %s: %w", id[:12], err)
	}
	return nil
}

// ContainerCopy extracts a tar archive at the root of the container,
// keeping the owners the archive gives.
func (docker *Client) ContainerCopy(ctx context.Context, id string, archive io.Reader) error {
	if _, err := docker.api.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: "/", Content: archive, CopyUIDGID: true}); err != nil {
		return fmt.Errorf("copying into %s: %w", id[:12], err)
	}
	return nil
}

// ContainerDelete stops the container and removes it.
func (docker *Client) ContainerDelete(ctx context.Context, id string) error {
	if _, err := docker.api.ContainerStop(ctx, id, client.ContainerStopOptions{}); err != nil && !IsNotFound(err) {
		return fmt.Errorf("stopping %s: %w", id[:12], err)
	}
	if _, err := docker.api.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true}); err != nil && !IsNotFound(err) {
		return fmt.Errorf("removing %s: %w", id[:12], err)
	}
	return nil
}

// ContainerFollow copies the container's output to output from the start,
// until it stops. Call it before ContainerStart, so nothing is missed. The
// channel closes once all of it is copied.
func (docker *Client) ContainerFollow(ctx context.Context, id string, output io.Writer) (<-chan struct{}, error) {
	attached, err := docker.api.ContainerAttach(ctx, id, client.ContainerAttachOptions{Stream: true, Stdout: true, Stderr: true, Logs: true})
	if err != nil {
		return nil, fmt.Errorf("attaching to %s: %w", id[:12], err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer attached.Close()
		_, _ = stdcopy.StdCopy(output, output, attached.Reader)
	}()
	return done, nil
}

// ContainerWait waits until the container stops and returns its exit code.
func (docker *Client) ContainerWait(ctx context.Context, id string) (int64, error) {
	wait := docker.api.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case result := <-wait.Result:
		if result.Error != nil {
			return result.StatusCode, fmt.Errorf("waiting for %s: %s", id[:12], result.Error.Message)
		}
		return result.StatusCode, nil
	case err := <-wait.Error:
		return 0, fmt.Errorf("waiting for %s: %w", id[:12], err)
	}
}

// ContainerCopyFrom returns a tar archive of the path in the container.
func (docker *Client) ContainerCopyFrom(ctx context.Context, id, path string) (io.ReadCloser, error) {
	result, err := docker.api.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: path})
	if err != nil {
		return nil, fmt.Errorf("copying %s from %s: %w", path, id[:12], err)
	}
	return result.Content, nil
}
