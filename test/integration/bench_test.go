//go:build integration

// Package integration runs the installer against a real Docker, the way gill
// does: the compose orchestrator on its engine, the swarm orchestrator on its
// swarm. Each test installs its own stack and checks that uninstalling it
// leaves nothing behind.
//
// Run it with make integration, on a Docker you can spare: it creates and
// removes services, containers, networks, volumes, configs and secrets.
package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/installer/compose"
	nemoswarm "github.com/sntns/nemo/pkg/installer/swarm"
	"github.com/sntns/nemo/pkg/labels"
	"github.com/sntns/nemo/pkg/progress"
)

// image is the only image the scenarios run, so that a run pulls one image.
const image = "busybox:1.37"

// timeout bounds each action, as nemo-timeout does.
const timeout = 2 * time.Minute

var orchestrators = installer.Orchestrators{
	compose.Name:   compose.New,
	nemoswarm.Name: nemoswarm.New,
}

// bench is one installation of one orchestrator. When the test ends it is
// uninstalled with its volumes, even if the test uninstalled it already.
type bench struct {
	t            *testing.T
	orchestrator string
	stack        string
	api          client.APIClient

	// credentials are delivered to the installer, each in the environment
	// variable of its name, as a runtime delivers a bundle's credentials.
	credentials map[string]string

	// timeout is the nemo-timeout of the actions.
	timeout time.Duration

	// after runs once the stack is uninstalled: it removes what a test
	// created outside of the stack.
	after []func()

	mu      sync.Mutex
	events  []progress.Event
	project string
}

func newBench(t *testing.T, orchestrator string) *bench {
	t.Helper()
	t.Parallel()
	connection, err := docker.Connect(t.Context())
	require.NoError(t, err)
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	b := &bench{t: t, orchestrator: orchestrator, stack: "it" + hex.EncodeToString(suffix), api: connection.API(), timeout: timeout}
	t.Cleanup(func() {
		defer connection.Close()
		defer b.logEventsOnFailure()
		if b.project != "" {
			assert.NoError(t, b.run(cnab.ActionUninstall, b.project, true), "uninstall")
			b.assertNothingLeft()
		}
		for _, after := range slices.Backward(b.after) {
			after()
		}
	})
	return b
}

func (b *bench) install(project string) error { return b.run(cnab.ActionInstall, project, false) }

func (b *bench) upgrade(project string) error { return b.run(cnab.ActionUpgrade, project, false) }

func (b *bench) uninstall(purge bool) error { return b.run(cnab.ActionUninstall, b.project, purge) }

func (b *bench) validate(project string) error { return b.run(cnab.ActionValidate, project, false) }

// run performs the action on the compose project, as gill does for a bundle
// that carries it.
func (b *bench) run(action, project string, purge bool) error {
	b.t.Helper()
	var config map[string]any
	require.NoError(b.t, yaml.Unmarshal([]byte(project), &config), "the compose project of the test")
	if action != cnab.ActionValidate {
		b.project = project
	}
	credentials := map[string]bundle.Credential{}
	for name := range b.credentials {
		credentials[name] = bundle.Credential{Location: bundle.Location{EnvironmentVariable: name}}
	}
	operation := installer.Operation{
		Installation: b.stack,
		Revision:     action,
		Action:       action,
		Bundle: cnab.ExtendedBundle{
			Bundle: bundle.Bundle{
				Name:             "bench",
				Version:          "0.0.1",
				InvocationImages: []bundle.InvocationImage{{BaseImage: bundle.BaseImage{ImageType: "docker", Image: image}}},
				Credentials:      credentials,
			},
			Compose: &cnab.Compose{Documents: []cnab.ComposeDocument{{Name: "compose.yaml", Config: config}}},
		},
		Parameters: map[string]any{
			cnab.ParameterOrchestrator: b.orchestrator,
			cnab.ParameterTimeout:      b.timeout.String(),
			cnab.ParameterPurge:        purge,
		},
		Credentials: b.credentials,
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(b.t.Context()), b.timeout+time.Minute)
	defer cancel()
	return installer.Run(ctx, operation, orchestrators, progress.SinkFunc(b.record))
}

func (b *bench) record(event progress.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
}

func (b *bench) logEventsOnFailure() {
	if !b.t.Failed() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, event := range b.events {
		b.t.Logf("%s %s %s: %s", event.Group, event.Item, event.State, event.Message)
	}
}

// messages returns what the installer reported about one item.
func (b *bench) messages(item string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var messages []string
	for _, event := range b.events {
		if event.Item == item && event.Message != "" {
			messages = append(messages, event.Message)
		}
	}
	return messages
}

func (b *bench) byStack() client.Filters {
	return client.Filters{}.Add("label", labels.Stack+"="+b.stack)
}

// name is what the orchestrators name an object of the stack.
func (b *bench) name(object string) string { return b.stack + "_" + object }

func (b *bench) service(name string) swarm.Service {
	b.t.Helper()
	inspected, err := b.api.ServiceInspect(b.t.Context(), b.name(name), client.ServiceInspectOptions{})
	require.NoError(b.t, err)
	return inspected.Service
}

func (b *bench) tasks(name string) []swarm.Task {
	b.t.Helper()
	list, err := b.api.TaskList(b.t.Context(), client.TaskListOptions{Filters: client.Filters{}.Add("service", b.name(name))})
	require.NoError(b.t, err)
	return list.Items
}

// assertNothingLeft checks that the stack left no object. Swarm removes the
// containers of a removed service, and each node its copy of a removed
// network, on their own: they get some time.
func (b *bench) assertNothingLeft() {
	b.t.Helper()
	ctx := context.WithoutCancel(b.t.Context())
	assert.EventuallyWithT(b.t, func(c *assert.CollectT) {
		containers, err := b.api.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: b.byStack()})
		if assert.NoError(c, err) {
			assert.Empty(c, containers.Items, "containers")
		}
		networks, err := b.api.NetworkList(ctx, client.NetworkListOptions{Filters: b.byStack()})
		if assert.NoError(c, err) {
			assert.Empty(c, networks.Items, "networks")
		}
	}, 30*time.Second, time.Second)

	volumes, err := b.api.VolumeList(ctx, client.VolumeListOptions{Filters: b.byStack()})
	if assert.NoError(b.t, err) {
		assert.Empty(b.t, volumes.Items, "volumes")
	}
	if b.orchestrator != nemoswarm.Name {
		return
	}
	services, err := b.api.ServiceList(ctx, client.ServiceListOptions{Filters: b.byStack()})
	if assert.NoError(b.t, err) {
		assert.Empty(b.t, services.Items, "services")
	}
	configs, err := b.api.ConfigList(ctx, client.ConfigListOptions{Filters: b.byStack()})
	if assert.NoError(b.t, err) {
		assert.Empty(b.t, configs.Items, "configs")
	}
	secrets, err := b.api.SecretList(ctx, client.SecretListOptions{Filters: b.byStack()})
	if assert.NoError(b.t, err) {
		assert.Empty(b.t, secrets.Items, "secrets")
	}
}

// externalNetwork creates a network outside of the stack, as an operator
// does for a stack to join it, and returns its name.
func (b *bench) externalNetwork() string {
	b.t.Helper()
	name := b.stack + "-outside"
	driver := "bridge"
	if b.orchestrator == nemoswarm.Name {
		driver = "overlay"
	}
	_, err := b.api.NetworkCreate(b.t.Context(), name, client.NetworkCreateOptions{Driver: driver, Attachable: true})
	require.NoError(b.t, err)
	b.after = append(b.after, func() {
		assert.EventuallyWithT(b.t, func(c *assert.CollectT) {
			_, err := b.api.NetworkRemove(context.WithoutCancel(b.t.Context()), name, client.NetworkRemoveOptions{})
			assert.NoError(c, err)
		}, 30*time.Second, time.Second)
	})
	return name
}

// externalVolume creates a volume outside of the stack and returns its name.
func (b *bench) externalVolume() string {
	b.t.Helper()
	name := b.stack + "-outside"
	_, err := b.api.VolumeCreate(b.t.Context(), client.VolumeCreateOptions{Name: name})
	require.NoError(b.t, err)
	b.after = append(b.after, func() {
		assert.EventuallyWithT(b.t, func(c *assert.CollectT) {
			_, err := b.api.VolumeRemove(context.WithoutCancel(b.t.Context()), name, client.VolumeRemoveOptions{})
			assert.NoError(c, err)
		}, 30*time.Second, time.Second)
	})
	return name
}

// freePort returns a TCP port nothing listens on now.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// failsUntil is a shell command that exits 1 until the given time, then
// runs: a service that waits for a dependency that is not ready yet. The
// dollars are doubled for the compose interpolation.
func failsUntil(ready time.Time) string {
	return fmt.Sprintf(`[ "$$(date +%%s)" -ge %d ] || exit 1; exec sleep 3600`, ready.Unix())
}
