package runtime

import (
	"archive/tar"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/cnabio/cnab-go/driver"
	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	cliflags "github.com/docker/cli/cli/flags"
	"github.com/docker/go-connections/tlsconfig"
	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/docker"
	"github.com/sntns/nemo/pkg/target"
)

// dockerSocket is where the target's daemon listens, on the target. The
// invocation image runs on that same daemon, so the socket is mounted from
// there, whatever address the runtime reaches the daemon at.
const dockerSocket = "/var/run/docker.sock"

// outputsDirectory is where the invocation image writes its outputs.
const outputsDirectory = "/cnab/app/outputs"

// dockerDriver runs the invocation image on the target's Docker over the
// Engine API, as cnab-go's docker driver does: the image pulled with the
// credentials of docker login and checked against the bundle's digest, the
// operation's files copied in, its output followed, its outputs read, and
// its container removed. A bundle that requires io.cnab.docker gets the
// target's socket, and runs privileged when it asks to.
type dockerDriver struct {
	docker      *docker.Client
	credentials *configfile.ConfigFile
	bundle      cnab.ExtendedBundle
}

func newDockerDriver(ctx context.Context, deploymentTarget target.Target, bundle cnab.ExtendedBundle) (driver.Driver, error) {
	cli, err := dockerCLI(deploymentTarget)
	if err != nil {
		return nil, err
	}
	if _, err := cli.Client().Ping(ctx, client.PingOptions{}); err != nil {
		return nil, fmt.Errorf("reaching the target's Docker at %s: %w", cli.DockerEndpoint().Host, err)
	}
	return &dockerDriver{docker: docker.New(cli.Client()), credentials: cli.ConfigFile(), bundle: bundle}, nil
}

func (dockerDriver *dockerDriver) Handles(imageType string) bool {
	return imageType == driver.ImageTypeDocker || imageType == driver.ImageTypeOCI
}

func (dockerDriver *dockerDriver) Run(ctx context.Context, operation *driver.Operation) (driver.OperationResult, error) {
	image := operation.Image.Image
	registryAuth, err := dockerDriver.registryAuth(image)
	if err != nil {
		return driver.OperationResult{}, err
	}
	if err := dockerDriver.docker.ImageEnsure(ctx, image, "", registryAuth); err != nil {
		return driver.OperationResult{}, err
	}
	inspected, err := dockerDriver.docker.ImageInspect(ctx, image)
	if err != nil {
		return driver.OperationResult{}, err
	}
	if digest := operation.Image.Digest; digest != "" && !slices.ContainsFunc(inspected.RepoDigests, func(repoDigest string) bool { return strings.HasSuffix(repoDigest, "@"+digest) }) {
		return driver.OperationResult{}, fmt.Errorf("the invocation image %s is not the one the bundle was built with, %s", image, digest)
	}

	environment := make([]string, 0, len(operation.Environment))
	for _, name := range slices.Sorted(maps.Keys(operation.Environment)) {
		environment = append(environment, name+"="+operation.Environment[name])
	}
	host := &container.HostConfig{}
	if cnab.DockerExtension.RequiredBy(dockerDriver.bundle.Bundle) {
		host.Mounts = append(host.Mounts, mount.Mount{Type: mount.TypeBind, Source: dockerSocket, Target: dockerSocket})
		host.Privileged = dockerDriver.bundle.Docker != nil && dockerDriver.bundle.Docker.Privileged
	}
	id, err := dockerDriver.docker.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     &container.Config{Image: image, Env: environment, Entrypoint: []string{"/cnab/app/run"}, AttachStdout: true, AttachStderr: true},
		HostConfig: host,
	})
	if err != nil {
		return driver.OperationResult{}, err
	}
	defer func() { _ = dockerDriver.docker.ContainerDelete(context.WithoutCancel(ctx), id) }()

	user := ""
	if inspected.Config != nil {
		user = inspected.Config.User
	}
	archive, err := filesArchive(operation.Files, userID(user))
	if err != nil {
		return driver.OperationResult{}, err
	}
	if err := dockerDriver.docker.ContainerCopy(ctx, id, archive); err != nil {
		return driver.OperationResult{}, err
	}
	followed, err := dockerDriver.docker.ContainerFollow(ctx, id, operation.Out)
	if err != nil {
		return driver.OperationResult{}, err
	}
	if err := dockerDriver.docker.ContainerStart(ctx, id); err != nil {
		return driver.OperationResult{}, err
	}
	code, err := dockerDriver.docker.ContainerWait(ctx, id)
	if err != nil {
		return driver.OperationResult{}, err
	}
	<-followed

	outputs, outputsErr := dockerDriver.outputs(ctx, id, operation)
	if code != 0 {
		return driver.OperationResult{Outputs: outputs}, errors.Join(fmt.Errorf("the installer exited with code %d", code), outputsErr)
	}
	return driver.OperationResult{Outputs: outputs}, outputsErr
}

// registryAuth returns the credentials docker login keeps for the image's registry, encoded.
func (dockerDriver *dockerDriver) registryAuth(image string) (string, error) {
	auth, found, err := registryCredentials(image, dockerDriver.credentials)
	if err != nil || !found {
		return "", err
	}
	return authconfig.Encode(auth)
}

// outputs reads the outputs the operation wants from the stopped container.
func (dockerDriver *dockerDriver) outputs(ctx context.Context, id string, operation *driver.Operation) (map[string]string, error) {
	outputs := map[string]string{}
	if len(operation.Outputs) == 0 {
		return outputs, nil
	}
	content, err := dockerDriver.docker.ContainerCopyFrom(ctx, id, outputsDirectory)
	if err != nil {
		return outputs, err
	}
	defer content.Close()
	archive := tar.NewReader(content)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return outputs, nil
		}
		if err != nil {
			return outputs, err
		}
		// The archive's names start at the outputs directory itself.
		if name, wanted := operation.Outputs[path.Join(path.Dir(outputsDirectory), header.Name)]; wanted && !header.FileInfo().IsDir() {
			data, err := io.ReadAll(archive)
			if err != nil {
				return outputs, err
			}
			outputs[name] = string(data)
		}
	}
}

// filesArchive is a tar archive of the operation's files at their absolute
// paths, readable by the image's user only.
func filesArchive(files map[string]string, uid int) (io.Reader, error) {
	var buffer bytes.Buffer
	archive := tar.NewWriter(&buffer)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if !path.IsAbs(name) {
			return nil, fmt.Errorf("file %s: its path must be absolute", name)
		}
		content := files[name]
		if err := archive.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o600, Size: int64(len(content)), Uid: uid}); err != nil {
			return nil, err
		}
		if _, err := archive.Write([]byte(content)); err != nil {
			return nil, err
		}
	}
	return &buffer, archive.Close()
}

// userID is the user an image runs as, root when it names none by number.
func userID(user string) int {
	uid, err := strconv.Atoi(strings.Split(user, ":")[0])
	if err != nil {
		return 0
	}
	return uid
}

// Connect returns the Engine API of the target's Docker, reached as the
// docker CLI reaches it: what a status or the logs read.
func Connect(deploymentTarget target.Target) (client.APIClient, error) {
	cli, err := dockerCLI(deploymentTarget)
	if err != nil {
		return nil, err
	}
	return cli.Client(), nil
}

// dockerCLI returns the Docker client of the target: at its host or through
// its docker context, else the environment's, as the docker CLI connects.
// Its own messages are dropped.
func dockerCLI(deploymentTarget target.Target) (command.Cli, error) {
	cli, err := command.NewDockerCli(command.WithCombinedStreams(io.Discard))
	if err != nil {
		return nil, fmt.Errorf("creating the Docker client: %w", err)
	}
	opts := cliflagsOptions()
	if deploymentTarget.Host != "" {
		opts.Hosts = []string{deploymentTarget.Host}
	}
	opts.Context = deploymentTarget.Context
	if err := cli.Initialize(opts); err != nil {
		return nil, fmt.Errorf("connecting to the target's Docker: %w", err)
	}
	return cli, nil
}

// cliflagsOptions are the docker CLI's connection options from the
// environment: DOCKER_TLS_VERIFY and DOCKER_CERT_PATH, as the CLI reads
// them, and its configuration directory.
func cliflagsOptions() *cliflags.ClientOptions {
	opts := cliflags.NewClientOptions()
	opts.ConfigDir = config.Dir()
	// Docker uses TLS when DOCKER_TLS_VERIFY is set to anything; false only skips the check.
	if verify, set := os.LookupEnv("DOCKER_TLS_VERIFY"); set && verify != "" {
		opts.TLS = true
		opts.TLSVerify, _ = strconv.ParseBool(verify)
		certificates := cmp.Or(os.Getenv("DOCKER_CERT_PATH"), opts.ConfigDir)
		opts.TLSOptions = &tlsconfig.Options{
			CAFile:   filepath.Join(certificates, cliflags.DefaultCaFile),
			CertFile: filepath.Join(certificates, cliflags.DefaultCertFile),
			KeyFile:  filepath.Join(certificates, cliflags.DefaultKeyFile),
		}
	}
	return opts
}
