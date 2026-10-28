//go:build integration

// Package e2e runs nemo as its users do: it builds bundles with gill built
// from this tree, imports a stack of three bundles that depend on one
// another, and brings it up and down with the nemo command.
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/labels"
)

// work holds nemo, gill and the bundles, built once for every test.
var work string

// bundles are the fixture bundles of a stack that works, each requiring the
// one before it.
var bundles = []string{"broker", "api", "web"}

// fixtures are every bundle of testdata: those, one validate refuses on
// Swarm, and one that never starts.
var fixtures = append(slices.Clone(bundles), "invalid", "stuck")

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "nemo-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	work = directory
	code := 1
	if err := build(); err != nil {
		fmt.Fprintln(os.Stderr, "building nemo, gill and the bundles:", err)
	} else {
		code = m.Run()
	}
	os.RemoveAll(work)
	os.Exit(code)
}

func build() error {
	root, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return err
	}
	tree := strings.TrimSpace(string(root))
	steps := [][]string{
		{"go", "build", "-o", filepath.Join(work, "nemo"), "./cmd/nemo"},
		{"docker", "buildx", "build", "-f", "build/gill/Dockerfile", "--provenance=false", "--output", "type=oci,dest=" + filepath.Join(work, "gill.oci") + ",tar=false", "."},
	}
	for _, bundle := range fixtures {
		steps = append(steps, []string{
			filepath.Join(work, "nemo"), "bundle", "build",
			"-f", filepath.Join(tree, "test", "e2e", "testdata", bundle, "compose.yaml"),
			"--version", "0.0.1",
			"--image", "busybox:1.37=docker://busybox:1.37",
			"--installer", "oci:" + filepath.Join(work, "gill.oci"),
			"-o", "oci:" + filepath.Join(work, "bundles", bundle+".oci"),
		})
	}
	for _, step := range steps {
		command := exec.Command(step[0], step[1:]...)
		command.Dir = tree
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w\n%s", strings.Join(step, " "), err, output)
		}
	}
	return nil
}

// stack is one stack of the fixture bundles, in a nemo home of its own.
type stack struct {
	t    *testing.T
	name string
	home string
	api  client.APIClient
}

func newStack(t *testing.T) *stack {
	t.Helper()
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	api, err := client.New(client.FromEnv)
	require.NoError(t, err)
	s := &stack{t: t, name: "e2e" + hex.EncodeToString(suffix), home: t.TempDir(), api: api}
	t.Cleanup(func() {
		defer api.Close()
		_, _ = s.nemo("down", "--purge")
		s.assertNothingLeft()
	})
	return s
}

// nemo runs the nemo command on the stack's home, and returns what it wrote.
func (s *stack) nemo(args ...string) (string, error) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.t.Context()), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(work, "nemo"), args...)
	command.Env = append(os.Environ(), "NEMO_HOME="+s.home, "NO_COLOR=1")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	err := command.Run()
	if err != nil {
		s.t.Logf("nemo %s:\n%s", strings.Join(args, " "), output.String())
	}
	return output.String(), err
}

// importManifest imports a stack of the fixture bundles on the orchestrator.
func (s *stack) importManifest(orchestrator string, port int, installations ...string) {
	s.t.Helper()
	manifest := fmt.Sprintf(`name: %s
target:
  orchestrator: %s
  configuration:
    images: load
    volumes:
      data: {}
parameters:
  region: e2e
  web-port: "%d"
credentials:
  - name: api-token
    source:
      value: s3cret
installations:
`, s.name, orchestrator, port)
	for _, installation := range installations {
		manifest += fmt.Sprintf("  - name: %s\n    bundle:\n      version: 0.0.1\n", installation)
	}
	path := filepath.Join(s.t.TempDir(), "stack.yaml")
	require.NoError(s.t, os.WriteFile(path, []byte(manifest), 0o644))
	_, err := s.nemo("stack", "import", path, "--remote", "oci:"+filepath.Join(work, "bundles", "{{ .Component }}.oci"), "--yes")
	require.NoError(s.t, err)
}

func (s *stack) byStack() client.Filters {
	return client.Filters{}.Add("label", labels.Stack+"="+s.name)
}

// versions is what changes when nemo touches the stack: the version of each
// Swarm service and the id of each container.
func (s *stack) versions() map[string]string {
	s.t.Helper()
	versions := map[string]string{}
	containers, err := s.api.ContainerList(s.t.Context(), client.ContainerListOptions{All: true, Filters: s.byStack()})
	require.NoError(s.t, err)
	for _, summary := range containers.Items {
		if summary.Labels["com.docker.swarm.task.id"] == "" {
			versions[summary.Names[0]] = summary.ID
		}
	}
	if services, err := s.api.ServiceList(s.t.Context(), client.ServiceListOptions{Filters: s.byStack()}); err == nil {
		for _, service := range services.Items {
			versions[service.Spec.Name] = fmt.Sprint(service.Version.Index)
		}
	}
	return versions
}

func (s *stack) assertNothingLeft() {
	s.t.Helper()
	ctx := context.WithoutCancel(s.t.Context())
	assert.EventuallyWithT(s.t, func(c *assert.CollectT) {
		containers, err := s.api.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: s.byStack()})
		if assert.NoError(c, err) {
			assert.Empty(c, containers.Items, "containers")
		}
		networks, err := s.api.NetworkList(ctx, client.NetworkListOptions{Filters: s.byStack()})
		if assert.NoError(c, err) {
			assert.Empty(c, networks.Items, "networks")
		}
		volumes, err := s.api.VolumeList(ctx, client.VolumeListOptions{Filters: s.byStack()})
		if assert.NoError(c, err) {
			assert.Empty(c, volumes.Items, "volumes")
		}
	}, time.Minute, time.Second)
	if services, err := s.api.ServiceList(ctx, client.ServiceListOptions{Filters: s.byStack()}); err == nil {
		assert.Empty(s.t, services.Items, "services")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func get(t *testing.T, port int) (int, string, error) {
	t.Helper()
	response, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/index.html", port))
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, strings.TrimSpace(string(body)), nil
}

// The stack's life as its users live it: up, up again, status, logs, down.
func TestStack_upAndDown(t *testing.T) {
	for _, orchestrator := range []string{"compose", "swarm"} {
		t.Run(orchestrator, func(t *testing.T) {
			s := newStack(t)
			port := freePort(t)
			s.importManifest(orchestrator, port, bundles...)

			_, err := s.nemo("up")
			require.NoError(t, err)

			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				status, body, err := get(t, port)
				if assert.NoError(c, err) {
					assert.Equal(c, http.StatusOK, status)
					assert.Equal(c, "served", body)
				}
			}, 30*time.Second, time.Second, "web serves its page")

			status, err := s.nemo("status")
			require.NoError(t, err)
			for _, bundle := range bundles {
				assert.Contains(t, status, bundle)
			}
			_, err = s.nemo("logs", "api")
			assert.NoError(t, err)

			before := s.versions()
			_, err = s.nemo("up")
			require.NoError(t, err)
			assert.Equal(t, before, s.versions(), "nemo up again changed the stack")

			_, err = s.nemo("down", "--purge")
			require.NoError(t, err)
			s.assertNothingLeft()
			_, err = s.nemo("down", "--purge")
			assert.NoError(t, err, "a stack already down")
		})
	}
}

// nemo up checks every bundle against the target before it installs the
// first: a bundle Swarm cannot run stops it before anything is created.
func TestStack_checksEveryBundleBeforeInstallingAny(t *testing.T) {
	s := newStack(t)
	s.importManifest("swarm", freePort(t), "broker", "invalid")

	output, err := s.nemo("up")

	require.Error(t, err)
	assert.Contains(t, output, "privileged")
	assert.Empty(t, s.versions(), "nothing was installed")
}

// A stack whose second bundle never starts: nemo up fails, and nemo down
// still removes the bundle it installed before.
func TestStack_downRemovesAStackThatFailedHalfway(t *testing.T) {
	for _, orchestrator := range []string{"compose", "swarm"} {
		t.Run(orchestrator, func(t *testing.T) {
			s := newStack(t)
			s.importManifest(orchestrator, freePort(t), "broker", "stuck")

			output, err := s.nemo("up")
			require.Error(t, err)
			assert.Contains(t, output, "stuck")
			assert.NotEmpty(t, s.versions(), "broker was installed")

			_, err = s.nemo("down", "--purge")
			require.NoError(t, err)
			s.assertNothingLeft()
		})
	}
}

// Changing a parameter upgrades the bundles that use it, and leaves the
// others as they are.
func TestStack_upgradesTheBundlesAParameterChangeReaches(t *testing.T) {
	for _, orchestrator := range []string{"compose", "swarm"} {
		t.Run(orchestrator, func(t *testing.T) {
			s := newStack(t)
			s.importManifest(orchestrator, freePort(t), bundles...)
			_, err := s.nemo("up")
			require.NoError(t, err)
			before := s.versions()

			port := freePort(t)
			_, err = s.nemo("stack", "set", "--param", fmt.Sprintf("web-port=%d", port))
			require.NoError(t, err)
			_, err = s.nemo("up")
			require.NoError(t, err)

			after := s.versions()
			for name, version := range before {
				if strings.Contains(name, "web") {
					assert.NotEqual(t, version, after[name], "%s was upgraded", name)
				} else {
					assert.Equal(t, version, after[name], "%s was left as it was", name)
				}
			}
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				status, body, err := get(t, port)
				if assert.NoError(c, err) {
					assert.Equal(c, http.StatusOK, status)
					assert.Equal(c, "served", body)
				}
			}, 30*time.Second, time.Second, "web serves on its new port")
		})
	}
}
