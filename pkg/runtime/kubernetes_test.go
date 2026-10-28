package runtime

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/driver"
	"github.com/docker/cli/cli/context"
	"github.com/docker/cli/cli/context/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDockerEnvironment(t *testing.T) {
	t.Parallel()
	endpoint := docker.Endpoint{EndpointMeta: docker.EndpointMeta{Host: "tcp://vm1:2376"}, TLSData: &context.TLSData{CA: []byte("ca"), Cert: []byte("cert"), Key: []byte("key")}}
	environment, files, err := dockerEnvironment(endpoint)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"DOCKER_HOST": "tcp://vm1:2376", "DOCKER_CERT_PATH": dockerTLSDirectory, "DOCKER_TLS_VERIFY": "1"}, environment)
	assert.Equal(t, "key", files[dockerTLSDirectory+"/key.pem"])

	environment, files, err = dockerEnvironment(docker.Endpoint{EndpointMeta: docker.EndpointMeta{Host: "tcp://dind:2375"}})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"DOCKER_HOST": "tcp://dind:2375"}, environment, "a Docker without TLS")
	assert.Empty(t, files)

	_, _, err = dockerEnvironment(docker.Endpoint{EndpointMeta: docker.EndpointMeta{Host: "ssh://deploy@vm1"}})
	require.ErrorContains(t, err, "over tcp:// only, not at ssh://deploy@vm1")
}

func TestKubernetesDriver_objects(t *testing.T) {
	t.Parallel()
	kubernetesDriver := &kubernetesDriver{
		namespace:   "nemo",
		client:      fake.NewClientset(),
		environment: map[string]string{"DOCKER_HOST": "tcp://vm1:2376"},
		files:       map[string]string{dockerTLSDirectory + "/ca.pem": "ca"},
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	secret, job := kubernetesDriver.objects(&driver.Operation{
		Installation: "demo/nginx",
		Action:       "install",
		Image:        bundle.InvocationImage{BaseImage: bundle.BaseImage{Image: "ghcr.io/sntns/gill:0.1.0", Digest: digest}},
		Environment:  map[string]string{"CNAB_ACTION": "install"},
		Files:        map[string]string{"/cnab/bundle.json": "{}"},
	})

	assert.Equal(t, "nemo-demo-nginx-install-", job.GenerateName)
	container := job.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "ghcr.io/sntns/gill:0.1.0@"+digest, container.Image, "pinned to its digest")
	assert.Equal(t, "install", secret.StringData["env.CNAB_ACTION"])
	assert.Equal(t, "tcp://vm1:2376", secret.StringData["env.DOCKER_HOST"], "the target's address, not a socket")

	mounted := map[string]string{}
	for _, mount := range container.VolumeMounts {
		mounted[mount.MountPath] = secret.StringData[mount.SubPath]
	}
	assert.Equal(t, map[string]string{"/cnab/bundle.json": "{}", dockerTLSDirectory + "/ca.pem": "ca"}, mounted, "each file at its path")
	assert.False(t, *job.Spec.Template.Spec.AutomountServiceAccountToken)
	assert.Zero(t, *job.Spec.BackoffLimit, "an action is not retried")
}

func TestKubernetesDriver_follow(t *testing.T) {
	t.Parallel()
	pod := func(phase corev1.PodPhase, waiting string) *corev1.Pod {
		status := corev1.ContainerStatus{Image: "ghcr.io/sntns/gill:0.1.0"}
		if waiting != "" {
			status.State.Waiting = &corev1.ContainerStateWaiting{Reason: waiting, Message: "not found"}
		}
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "nemo-demo-pod", Namespace: "nemo", Labels: map[string]string{"job-name": "nemo-demo"}},
			Status:     corev1.PodStatus{Phase: phase, ContainerStatuses: []corev1.ContainerStatus{status}},
		}
	}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "nemo-demo", Namespace: "nemo"}, Status: batchv1.JobStatus{Failed: 1}}

	var output bytes.Buffer
	ran := &kubernetesDriver{namespace: "nemo", client: fake.NewClientset(pod(corev1.PodFailed, ""), job)}
	require.EqualError(t, ran.follow(t.Context(), "nemo-demo", &output), "the installer failed")
	assert.Equal(t, "fake logs", output.String(), "the pod's output")

	unpulled := &kubernetesDriver{namespace: "nemo", client: fake.NewClientset(pod(corev1.PodPending, "ErrImagePull"), job)}
	require.ErrorContains(t, unpulled.follow(t.Context(), "nemo-demo", &output), "the cluster can't pull the installer's image ghcr.io/sntns/gill:0.1.0: not found")
}
