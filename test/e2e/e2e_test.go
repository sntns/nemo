//go:build integration

// Package e2e runs nemo as its users do: it builds bundles with gill built
// from this tree, imports a stack of three bundles that depend on one
// another, and brings it up and down with the nemo command.
package e2e

import (
	"bytes"
	"cmp"
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
	"github.com/sntns/nemo/pkg/storage/volume"
)

// work holds nemo, gill and the bundles, built once for every test.
var work string

// registry is the address of a registry the bench runs, holding the bundles
// of the stack that works: localhost:PORT.
var registry string

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
	os.Exit(run(m))
}

func run(m *testing.M) int {
	defer os.RemoveAll(work)
	stop, err := startRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting a registry:", err)
		return 1
	}
	defer stop()
	if err := build(); err != nil {
		fmt.Fprintln(os.Stderr, "building nemo, gill and the bundles:", err)
		return 1
	}
	code := m.Run()
	if err := coverage(); err != nil {
		fmt.Fprintln(os.Stderr, "writing the coverage of nemo:", err)
	}
	return code
}

// startRegistry runs a registry on a free port of the loopback, which nemo
// pushes to and the daemon pulls from without TLS, as 127.0.0.1: localhost
// may resolve to ::1, where the port is not published.
func startRegistry() (func(), error) {
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	name := "nemo-e2e-registry-" + hex.EncodeToString(suffix)
	if output, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-p", "127.0.0.1::5000", "registry:2").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, output)
	}
	stop := func() { _ = exec.Command("docker", "rm", "-f", name).Run() }
	port, err := exec.Command("docker", "port", name, "5000").Output()
	if err != nil {
		stop()
		return nil, err
	}
	first := strings.TrimSpace(strings.Split(string(port), "\n")[0])
	number := first[strings.LastIndex(first, ":")+1:]
	registry = "127.0.0.1:" + number
	return stop, nil
}

// coverage writes what the runs of nemo covered to NEMO_E2E_COVERAGE, when set.
func coverage() error {
	profile := os.Getenv("NEMO_E2E_COVERAGE")
	if profile == "" {
		return nil
	}
	return exec.Command("go", "tool", "covdata", "textfmt", "-i", filepath.Join(work, "cover"), "-o", profile).Run()
}

func build() error {
	root, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return err
	}
	tree := strings.TrimSpace(string(root))
	// busybox is exported once, through the daemon's cache, rather than pulled by each bundle.
	busybox := filepath.Join(work, "busybox")
	if err := os.MkdirAll(busybox, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(busybox, "Dockerfile"), []byte("FROM busybox:1.37\n"), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(work, "cover"), 0o755); err != nil {
		return err
	}
	steps := [][]string{
		{"go", "build", "-cover", "-coverpkg=./pkg/...,./cmd/nemo", "-o", filepath.Join(work, "nemo"), "./cmd/nemo"},
		{"docker", "buildx", "build", "-f", "build/gill/Dockerfile", "--provenance=false", "--output", "type=oci,dest=" + filepath.Join(work, "gill.oci") + ",tar=false", "."},
		{"docker", "buildx", "build", "--provenance=false", "--output", "type=oci,dest=" + filepath.Join(work, "busybox.oci") + ",tar=false", busybox},
	}
	for _, bundle := range fixtures {
		steps = append(steps, []string{
			filepath.Join(work, "nemo"), "bundle", "build",
			"-f", filepath.Join(tree, "test", "e2e", "testdata", bundle, "compose.yaml"),
			"--version", "0.0.1",
			"--image", "busybox:1.37=oci:" + filepath.Join(work, "busybox.oci"),
			"--installer", "oci:" + filepath.Join(work, "gill.oci"),
			"-o", "oci:" + filepath.Join(work, "bundles", bundle+".oci"),
		})
	}
	for _, bundle := range bundles {
		steps = append(steps, []string{
			filepath.Join(work, "nemo"), "bundle", "copy",
			"oci:" + filepath.Join(work, "bundles", bundle+".oci"),
			"docker://" + registry + "/bench/" + bundle + ":0.0.1",
		})
	}
	for _, step := range steps {
		command := exec.Command(step[0], step[1:]...)
		command.Dir = tree
		command.Env = append(os.Environ(), "GOCOVERDIR="+filepath.Join(work, "cover"))
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

	// after runs once the stack is down: it removes what a test left
	// outside of it.
	after []func()
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
		for _, after := range slices.Backward(s.after) {
			after()
		}
	})
	return s
}

// nemo runs the nemo command on the stack's home, and returns what it wrote.
func (s *stack) nemo(args ...string) (string, error) {
	s.t.Helper()
	return s.nemoAt(s.home, args...)
}

// nemoAt runs the nemo command on another home, another machine's.
func (s *stack) nemoAt(home string, args ...string) (string, error) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.t.Context()), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(work, "nemo"), args...)
	command.Env = append(os.Environ(), "NEMO_HOME="+home, "NO_COLOR=1", "GOCOVERDIR="+filepath.Join(work, "cover"))
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	err := command.Run()
	if err != nil {
		s.t.Logf("nemo %s:\n%s", strings.Join(args, " "), output.String())
	}
	return output.String(), err
}

// from is where a stack takes its bundles and their images, and the volume
// types of its target; empty, the bundles' layouts, loaded, and plain volumes.
type from struct {
	remote string
	images string
	// volumes is the YAML of the target's volume types, indented under volumes.
	volumes string
	// records is where nemo keeps the records of the installations: home or target.
	records string
}

// importManifest imports a stack of the fixture bundles on the orchestrator.
func (s *stack) importManifest(orchestrator string, port int, installations ...string) {
	s.t.Helper()
	s.importManifestFrom(from{}, orchestrator, port, installations...)
}

// importManifestFrom imports the stack, and returns its manifest, for
// another home to import it too.
func (s *stack) importManifestFrom(where from, orchestrator string, port int, installations ...string) string {
	s.t.Helper()
	manifest := fmt.Sprintf(`name: %s
records: %s
target:
  orchestrator: %s
  configuration:
    images: %s
    volumes:
%s
parameters:
  region: e2e
  web-port: "%d"
credentials:
  - name: api-token
    source:
      value: s3cret
installations:
`, s.name, cmp.Or(where.records, "home"), orchestrator, cmp.Or(where.images, "load"), cmp.Or(where.volumes, "      data: {}"), port)
	for _, installation := range installations {
		manifest += fmt.Sprintf("  - name: %s\n    bundle:\n      version: 0.0.1\n", installation)
	}
	path := filepath.Join(s.t.TempDir(), "stack.yaml")
	require.NoError(s.t, os.WriteFile(path, []byte(manifest), 0o644))
	remote := cmp.Or(where.remote, "oci:"+filepath.Join(work, "bundles", "{{ .Component }}.oci"))
	_, err := s.nemo("stack", "import", path, "--remote", remote, "--yes")
	require.NoError(s.t, err)
	return path
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

// served waits until web serves its page on the port.
func served(t *testing.T, port int) {
	t.Helper()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		status, body, err := get(t, port)
		if assert.NoError(c, err) {
			assert.Equal(c, http.StatusOK, status)
			assert.Equal(c, "served", body)
		}
	}, 30*time.Second, time.Second, "web serves its page")
}

// The stack's bundles come from a registry, as a release's do: nemo pulls
// each by its tag, and the target pulls their images, which nemo bundle copy
// put next to them, by digest.
func TestStack_fromARegistry(t *testing.T) {
	if os, _ := exec.Command("docker", "info", "--format", "{{.OperatingSystem}}").Output(); strings.TrimSpace(string(os)) == "Docker Desktop" {
		t.Skip("Docker Desktop's daemon runs in a machine of its own, whose loopback does not reach a published port: the CI runs it")
	}
	for _, orchestrator := range []string{"compose", "swarm"} {
		t.Run(orchestrator, func(t *testing.T) {
			s := newStack(t)
			port := freePort(t)
			s.importManifestFrom(from{remote: "docker://" + registry + "/bench/{{ .Component }}:{{ .Version }}", images: "registry"}, orchestrator, port, bundles...)

			_, err := s.nemo("up")
			require.NoError(t, err)
			served(t, port)

			containers, err := s.api.ContainerList(t.Context(), client.ContainerListOptions{Filters: s.byStack()})
			require.NoError(t, err)
			require.NotEmpty(t, containers.Items)
			for _, summary := range containers.Items {
				assert.True(t, strings.HasPrefix(summary.Image, registry+"/bench/"), "%s runs %s, pulled from the registry", summary.Names[0], summary.Image)
			}
		})
	}
}

// storage mounts /tmp in a volume, as the volume type does, and runs the
// shell command there. Docker Desktop binds the volume to the /tmp it shares
// with the machine nemo runs on, the same for every volume it creates.
func storage(t *testing.T, command string) (string, error) {
	t.Helper()
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	volume := "nemo-e2e-storage-" + hex.EncodeToString(suffix)
	if output, err := exec.Command("docker", "volume", "create", "--driver", "local", "--opt", "type=none", "--opt", "o=bind", "--opt", "device=/tmp", volume).CombinedOutput(); err != nil {
		return string(output), err
	}
	defer func() { _ = exec.Command("docker", "volume", "rm", volume).Run() }()
	output, err := exec.Command("docker", "run", "--rm", "-v", volume+":/storage", "busybox:1.37", "sh", "-c", command).CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

// A volume type whose directory gill creates before the volume, as for a
// directory of the host or of an NFS storage.
func TestStack_provisionsTheDirectoryOfAVolume(t *testing.T) {
	for _, orchestrator := range []string{"compose", "swarm"} {
		t.Run(orchestrator, func(t *testing.T) {
			s := newStack(t)
			s.after = append(s.after, func() { _, _ = storage(t, "rm -rf /storage/nemo-e2e/"+s.name) })
			port := freePort(t)
			s.importManifestFrom(from{volumes: `      data:
        driver: local
        options: { type: none, o: bind, device: "/tmp/nemo-e2e/{{.Stack}}/{{.Name}}" }
        provision:
          directory: "nemo-e2e/{{.Stack}}/{{.Name}}"
          volume:
            driver: local
            options: { type: none, o: bind, device: /tmp }`}, orchestrator, port, bundles...)

			_, err := s.nemo("up")
			require.NoError(t, err)
			served(t, port)

			page, err := storage(t, "cat /storage/nemo-e2e/"+s.name+"/content/index.html")
			require.NoError(t, err, page)
			assert.Equal(t, "served", page, "web wrote its page in the directory gill provisioned")
		})
	}
}

// A stack that keeps its records on its target is the same stack from every
// home that imported it: a second machine sees what the first deployed. One
// nemo writes it at a time, and a stack Terraform manages is not written
// without --force.
func TestStack_keepsItsRecordsOnTheTarget(t *testing.T) {
	s := newStack(t)
	s.after = append(s.after, func() {
		_, _ = s.api.VolumeRemove(context.WithoutCancel(t.Context()), volume.Name(s.name), client.VolumeRemoveOptions{Force: true})
	})
	port := freePort(t)
	remote := "oci:" + filepath.Join(work, "bundles", "{{ .Component }}.oci")
	manifest := s.importManifestFrom(from{records: "target"}, "compose", port, bundles...)
	_, err := s.nemo("up")
	require.NoError(t, err)

	other := t.TempDir()
	_, err = s.nemoAt(other, "stack", "import", manifest, "--remote", remote, "--yes")
	require.NoError(t, err)
	status, err := s.nemoAt(other, "status")
	require.NoError(t, err)
	for _, bundle := range bundles {
		assert.Contains(t, status, bundle, "the other home reads the target's records")
	}
	runs, err := s.nemoAt(other, "installation", "runs", "web")
	require.NoError(t, err)
	assert.Contains(t, runs, "install", "and the runs of the first")

	held, err := volume.Open(t.Context(), s.api, s.name, nil, true)
	require.NoError(t, err)
	output, err := s.nemo("up")
	assert.Error(t, err)
	assert.Contains(t, output, "is busy: another nemo writes it since")
	_, err = s.nemo("status")
	assert.NoError(t, err, "reading is not writing")
	require.NoError(t, held.SetOwner(t.Context(), "terraform"))
	require.NoError(t, held.Close(t.Context()))

	output, err = s.nemoAt(other, "up")
	assert.Error(t, err)
	assert.Contains(t, output, "stack "+s.name+" is managed by terraform")
	_, err = s.nemoAt(other, "up", "--force")
	assert.NoError(t, err, "forced")
	_, err = s.nemoAt(other, "down", "--purge", "--force")
	require.NoError(t, err)

	list, err := s.nemo("installation", "list")
	require.NoError(t, err)
	assert.Equal(t, len(bundles), strings.Count(list, "uninstalled"), "the first home sees the down of the other")
}
