package runtime

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/driver"
	"github.com/docker/cli/cli/config/configfile"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/docker"
)

// fakeEngine is a daemon that runs one container: it records what it was
// given, prints a line, writes an output, and exits with code.
type fakeEngine struct {
	client.APIClient
	repoDigests []string
	code        int64
	created     client.ContainerCreateOptions
	files       map[string]*tar.Header
	removed     bool
}

func (fake *fakeEngine) ImageInspect(ctx context.Context, name string, options ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	config := &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{User: "1000:1000"}}
	return client.ImageInspectResult{InspectResponse: image.InspectResponse{RepoDigests: fake.repoDigests, Config: config}}, nil
}

func (fake *fakeEngine) ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	fake.created = options
	return client.ContainerCreateResult{ID: strings.Repeat("c", 64)}, nil
}

func (fake *fakeEngine) CopyToContainer(ctx context.Context, id string, options client.CopyToContainerOptions) (client.CopyToContainerResult, error) {
	fake.files = map[string]*tar.Header{}
	archive := tar.NewReader(options.Content)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return client.CopyToContainerResult{}, nil
		}
		if err != nil {
			return client.CopyToContainerResult{}, err
		}
		fake.files[header.Name] = header
	}
}

func (fake *fakeEngine) ContainerAttach(ctx context.Context, id string, options client.ContainerAttachOptions) (client.ContainerAttachResult, error) {
	server, connection := net.Pipe()
	go func() {
		line := []byte("services: web running\n")
		// A frame of Docker's multiplexed stream: the stream, then the size.
		frame := append([]byte{byte(stdcopy.Stdout), 0, 0, 0}, binary.BigEndian.AppendUint32(nil, uint32(len(line)))...)
		_, _ = server.Write(append(frame, line...))
		server.Close()
	}()
	return client.ContainerAttachResult{HijackedResponse: client.NewHijackedResponse(connection, "")}, nil
}

func (fake *fakeEngine) ContainerStart(ctx context.Context, id string, options client.ContainerStartOptions) (client.ContainerStartResult, error) {
	return client.ContainerStartResult{}, nil
}

func (fake *fakeEngine) ContainerWait(ctx context.Context, id string, options client.ContainerWaitOptions) client.ContainerWaitResult {
	result := make(chan container.WaitResponse, 1)
	result <- container.WaitResponse{StatusCode: fake.code}
	return client.ContainerWaitResult{Result: result}
}

func (fake *fakeEngine) CopyFromContainer(ctx context.Context, id string, options client.CopyFromContainerOptions) (client.CopyFromContainerResult, error) {
	var buffer bytes.Buffer
	archive := tar.NewWriter(&buffer)
	_ = archive.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "outputs/", Mode: 0o755})
	_ = archive.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "outputs/url", Mode: 0o644, Size: 11})
	_, _ = archive.Write([]byte("http://demo"))
	_ = archive.Close()
	return client.CopyFromContainerResult{Content: io.NopCloser(&buffer)}, nil
}

func (fake *fakeEngine) ContainerStop(ctx context.Context, id string, options client.ContainerStopOptions) (client.ContainerStopResult, error) {
	return client.ContainerStopResult{}, nil
}

func (fake *fakeEngine) ContainerRemove(ctx context.Context, id string, options client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	fake.removed = true
	return client.ContainerRemoveResult{}, nil
}

func TestDockerDriver(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("a", 64)
	newOperation := func(output io.Writer) *driver.Operation {
		return &driver.Operation{
			Image:       bundle.InvocationImage{BaseImage: bundle.BaseImage{Image: "gill:dev", Digest: digest}},
			Environment: map[string]string{"CNAB_ACTION": "install", "CNAB_INSTALLATION_NAME": "demo/nginx"},
			Files:       map[string]string{"/cnab/bundle.json": "{}", "/cnab/app/credentials/token": "s3cret"},
			Outputs:     map[string]string{"/cnab/app/outputs/url": "url"},
			Out:         output,
		}
	}

	fake := &fakeEngine{repoDigests: []string{"gill@" + digest}}
	dockerDriver := &dockerDriver{docker: docker.New(fake), credentials: configfile.New(""), bundle: testBundle(t)}
	var output bytes.Buffer
	result, err := dockerDriver.Run(context.Background(), newOperation(&output))
	require.NoError(t, err)

	assert.Equal(t, []string{"/cnab/app/run"}, []string(fake.created.Config.Entrypoint))
	assert.Equal(t, []string{"CNAB_ACTION=install", "CNAB_INSTALLATION_NAME=demo/nginx"}, fake.created.Config.Env)
	assert.Equal(t, dockerSocket, fake.created.HostConfig.Mounts[0].Target, "io.cnab.docker: the target's socket")
	assert.Equal(t, 1000, fake.files["/cnab/app/credentials/token"].Uid, "the image's user reads its files")
	assert.Equal(t, int64(0o600), fake.files["/cnab/app/credentials/token"].Mode)
	assert.Equal(t, "services: web running\n", output.String(), "the installer's output, all of it")
	assert.Equal(t, map[string]string{"url": "http://demo"}, result.Outputs)
	assert.True(t, fake.removed, "the container is removed")

	fake.code = 3
	_, err = dockerDriver.Run(context.Background(), newOperation(io.Discard))
	require.EqualError(t, err, "the installer exited with code 3")

	fake.repoDigests = []string{"gill@sha256:" + strings.Repeat("b", 64)}
	_, err = dockerDriver.Run(context.Background(), newOperation(io.Discard))
	require.ErrorContains(t, err, "is not the one the bundle was built with")
}
