package nemo

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"golang.org/x/sync/errgroup"

	"github.com/sntns/nemo/pkg/labels"
	"github.com/sntns/nemo/pkg/runtime"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
)

// StackStatus is where a stack stands: each installation as nemo keeps
// it, and its services as the target runs them.
type StackStatus struct {
	Name          string              `json:"name"`
	Target        target.Target       `json:"target"`
	Installations []InstallationState `json:"installations"`
}

// InstallationState is where an installation stands: its last action, and
// its services.
type InstallationState struct {
	Name      string         `json:"name"`
	Version   string         `json:"version"`
	Installed bool           `json:"installed"`
	Action    string         `json:"action"`
	Result    string         `json:"result"`
	Modified  time.Time      `json:"modified"`
	Services  []ServiceState `json:"services,omitempty"`
}

// ServiceState is a service as the target runs it: its tasks or containers
// running out of those wanted, and the state of the least advanced.
type ServiceState struct {
	Name    string `json:"name"`
	Running int    `json:"running"`
	Desired int    `json:"desired"`
	State   string `json:"state"`
}

// Status reads the installations of the stack, then the services they run
// on their target.
func (nemo *Nemo) Status(ctx context.Context, stack string) (StackStatus, error) {
	installations, deployment, err := nemo.stackOf(ctx, stack)
	if err != nil {
		return StackStatus{}, err
	}
	api, err := runtime.Connect(deployment)
	if err != nil {
		return StackStatus{}, err
	}
	defer api.Close()
	services, err := servicesOf(ctx, api, deployment.Orchestrator, client.Filters{}.Add("label", labels.Stack+"="+stack))
	if err != nil {
		return StackStatus{}, err
	}

	status := StackStatus{Name: stack, Target: deployment}
	for _, installation := range installations {
		state := InstallationState{
			Name:      installation.Name,
			Version:   installation.Status.BundleVersion,
			Installed: installation.IsInstalled(),
			Action:    installation.Status.Action,
			Result:    installation.Status.ResultStatus,
			Modified:  installation.Status.Modified,
		}
		for _, service := range services {
			if service.bundle == installation.Name {
				state.Services = append(state.Services, service.state)
			}
		}
		slices.SortFunc(state.Services, func(left, right ServiceState) int { return cmp.Compare(left.Name, right.Name) })
		status.Installations = append(status.Installations, state)
	}
	return status, nil
}

// stackOf returns the installations of the stack, and the target they are
// deployed on: a stack has one.
func (nemo *Nemo) stackOf(ctx context.Context, stack string) ([]storage.Installation, target.Target, error) {
	installations, err := nemo.store.ListInstallations(ctx, stack)
	if err != nil {
		return nil, target.Target{}, err
	}
	if len(installations) == 0 {
		return nil, target.Target{}, fmt.Errorf("stack %s has no installations", stack)
	}
	deployment := installations[0].Target
	for _, installation := range installations {
		if installation.IsInstalled() {
			deployment = installation.Target
		}
	}
	return installations, deployment, nil
}

// service is a service of the target, with the installation it belongs to
// and what opens its logs: one multiplexed stream, or one per container
// with Compose.
type service struct {
	bundle  string
	state   ServiceState
	streams func(ctx context.Context, options client.ContainerLogsOptions) ([]io.ReadCloser, error)
}

// servicesOf reads the services the filters select: Swarm's services, or
// the containers Compose runs for each service, by their labels.
func servicesOf(ctx context.Context, api client.APIClient, orchestrator string, filters client.Filters) ([]service, error) {
	if orchestrator == target.Swarm {
		listed, err := api.ServiceList(ctx, client.ServiceListOptions{Filters: filters, Status: true})
		if err != nil {
			return nil, fmt.Errorf("listing the services: %w", err)
		}
		services := make([]service, 0, len(listed.Items))
		for _, item := range listed.Items {
			running, desired := 0, 0
			if status := item.ServiceStatus; status != nil {
				running, desired = int(status.RunningTasks), int(status.DesiredTasks)
			}
			state := "running"
			if running < desired {
				state = "starting"
			}
			id := item.ID
			services = append(services, service{
				bundle: item.Spec.Labels[labels.Bundle],
				state:  ServiceState{Name: item.Spec.Labels[labels.Service], Running: running, Desired: desired, State: state},
				streams: func(ctx context.Context, options client.ContainerLogsOptions) ([]io.ReadCloser, error) {
					logs, err := api.ServiceLogs(ctx, id, client.ServiceLogsOptions(options))
					return []io.ReadCloser{logs}, err
				},
			})
		}
		return services, nil
	}

	listed, err := api.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("listing the containers: %w", err)
	}
	byName := map[string]*service{}
	var containers []container.Summary
	for _, summary := range listed.Items {
		key := summary.Labels[labels.Bundle] + "/" + summary.Labels[labels.Service]
		found, ok := byName[key]
		if !ok {
			found = &service{bundle: summary.Labels[labels.Bundle], state: ServiceState{Name: summary.Labels[labels.Service], State: "running"}}
			byName[key] = found
		}
		found.state.Desired++
		state := string(summary.State)
		if summary.Health != nil && summary.Health.Status != "" && summary.Health.Status != container.NoHealthcheck {
			state = string(summary.Health.Status)
		}
		if summary.State == container.StateRunning {
			found.state.Running++
		}
		if state != "running" && state != "healthy" {
			found.state.State = state
		}
		containers = append(containers, summary)
	}
	services := make([]service, 0, len(byName))
	for _, key := range slices.Sorted(maps.Keys(byName)) {
		found := byName[key]
		var ids []string
		for _, summary := range containers {
			if summary.Labels[labels.Bundle]+"/"+summary.Labels[labels.Service] == key {
				ids = append(ids, summary.ID)
			}
		}
		found.streams = func(ctx context.Context, options client.ContainerLogsOptions) ([]io.ReadCloser, error) {
			var streams []io.ReadCloser
			for _, id := range ids {
				logs, err := api.ContainerLogs(ctx, id, options)
				if err != nil {
					for _, opened := range streams {
						opened.Close()
					}
					return nil, err
				}
				streams = append(streams, logs)
			}
			return streams, nil
		}
		services = append(services, *found)
	}
	return services, nil
}

// LogsRequest follows the logs of the services of a stack.
type LogsRequest struct {
	Stack string
	// Installation and Service narrow the services; empty, every one.
	Installation string
	Service      string
	Follow       bool
	// Tail is how many lines of each service to start with: a number, or all.
	Tail string
	// Line receives each line with its service, from as many goroutines as
	// there are services.
	Line func(service, line string)
}

// Logs writes the logs of the services until they end, or until the
// context is done when following them.
func (nemo *Nemo) Logs(ctx context.Context, request LogsRequest) error {
	installations, deployment, err := nemo.stackOf(ctx, request.Stack)
	if err != nil {
		return err
	}
	if request.Installation != "" && !slices.ContainsFunc(installations, func(installation storage.Installation) bool { return installation.Name == request.Installation }) {
		return fmt.Errorf("stack %s has no installation %s", request.Stack, request.Installation)
	}
	api, err := runtime.Connect(deployment)
	if err != nil {
		return err
	}
	defer api.Close()
	filters := client.Filters{}.Add("label", labels.Stack+"="+request.Stack)
	if request.Installation != "" {
		filters = filters.Add("label", labels.Bundle+"="+request.Installation)
	}
	if request.Service != "" {
		filters = filters.Add("label", labels.Service+"="+request.Service)
	}
	services, err := servicesOf(ctx, api, deployment.Orchestrator, filters)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return fmt.Errorf("stack %s runs no service %s", request.Stack, cmp.Or(request.Service, "of "+request.Installation))
	}

	tail := cmp.Or(request.Tail, "all")
	if _, err := strconv.Atoi(tail); err != nil && tail != "all" {
		return fmt.Errorf("tail %s: a number of lines, or all", tail)
	}
	options := client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: request.Follow, Tail: tail}
	// Every stream is opened before any is read, so that none is left behind on an error.
	type stream struct {
		service string
		logs    io.ReadCloser
	}
	var streams []stream
	for _, service := range services {
		opened, err := service.streams(ctx, options)
		if err != nil {
			for _, stream := range streams {
				stream.logs.Close()
			}
			return fmt.Errorf("the logs of %s: %w", service.state.Name, err)
		}
		for _, logs := range opened {
			streams = append(streams, stream{service: service.state.Name, logs: logs})
		}
	}
	var group errgroup.Group
	for _, stream := range streams {
		group.Go(func() error {
			defer stream.logs.Close()
			reader, writer := io.Pipe()
			go func() { writer.CloseWithError(demultiplex(writer, stream.logs)) }()
			scanner := bufio.NewScanner(reader)
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)
			for scanner.Scan() {
				request.Line(stream.service, scanner.Text())
			}
			return scanner.Err()
		})
	}
	err = group.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// demultiplex copies the output and the errors of a multiplexed stream.
func demultiplex(output io.Writer, logs io.Reader) error {
	_, err := stdcopy.StdCopy(output, output, logs)
	return err
}
