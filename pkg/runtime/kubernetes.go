package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cnabio/cnab-go/driver"
	dockercontext "github.com/docker/cli/cli/context"
	"github.com/docker/cli/cli/context/docker"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/sntns/nemo/pkg/target"
)

// dockerTLSDirectory is where the Job finds the TLS files of the target's Docker.
const dockerTLSDirectory = "/cnab/app/docker-tls"

// kubernetesDriver runs the invocation image as a Job in a cluster. The
// installer then runs away from the target and gets no socket: the runtime
// honours io.cnab.docker by handing it the target's address and TLS files,
// which it connects to like any Docker of its environment.
//
// cnab-go's own kubernetes driver shares files through a volume mounted
// where it runs, which a CLI outside the cluster does not have: this one
// carries the operation's environment and files in a Secret.
type kubernetesDriver struct {
	namespace string
	client    kubernetes.Interface
	// environment and files reach the target's Docker from the cluster.
	environment map[string]string
	files       map[string]string
}

func newKubernetesDriver(deploymentTarget target.Target, settings target.Kubernetes) (driver.Driver, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = settings.Kubeconfig
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("reaching the cluster: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("reaching the cluster: %w", err)
	}

	cli, err := dockerCLI(deploymentTarget)
	if err != nil {
		return nil, err
	}
	endpoint := cli.DockerEndpoint()
	if given := deploymentTarget.TLS; given != nil {
		endpoint.TLSData = &dockercontext.TLSData{CA: []byte(given.CA), Cert: []byte(given.Cert), Key: []byte(given.Key)}
	}
	environment, files, err := dockerEnvironment(endpoint)
	if err != nil {
		return nil, err
	}
	return &kubernetesDriver{namespace: settings.Namespace, client: client, environment: environment, files: files}, nil
}

// dockerEnvironment returns what the installer needs to reach the target's
// Docker from the cluster: DOCKER_HOST, and the TLS files when there are.
func dockerEnvironment(endpoint docker.Endpoint) (map[string]string, map[string]string, error) {
	if !strings.HasPrefix(endpoint.Host, "tcp://") {
		return nil, nil, fmt.Errorf("the installer runs in the cluster, which reaches the target's Docker over tcp:// only, not at %s", endpoint.Host)
	}
	environment := map[string]string{"DOCKER_HOST": endpoint.Host}
	files := map[string]string{}
	if endpoint.TLSData != nil {
		environment["DOCKER_CERT_PATH"] = dockerTLSDirectory
		if !endpoint.SkipTLSVerify {
			environment["DOCKER_TLS_VERIFY"] = "1"
		}
		for name, data := range map[string][]byte{"ca.pem": endpoint.TLSData.CA, "cert.pem": endpoint.TLSData.Cert, "key.pem": endpoint.TLSData.Key} {
			if len(data) > 0 {
				files[dockerTLSDirectory+"/"+name] = string(data)
			}
		}
	}
	return environment, files, nil
}

func (kubernetesDriver *kubernetesDriver) Handles(imageType string) bool {
	return imageType == driver.ImageTypeDocker || imageType == driver.ImageTypeOCI
}

// Run creates the Secret and the Job, streams the installer's output, and
// removes both once the Job is done.
func (kubernetesDriver *kubernetesDriver) Run(ctx context.Context, operation *driver.Operation) (driver.OperationResult, error) {
	if len(operation.Outputs) > 0 {
		return driver.OperationResult{}, fmt.Errorf("driver %s: bundle outputs: %w", target.DriverKubernetes, ErrNotImplemented)
	}
	secret, job := kubernetesDriver.objects(operation)
	cleanup := context.WithoutCancel(ctx)
	background := metav1.DeletePropagationBackground

	secret, err := kubernetesDriver.client.CoreV1().Secrets(kubernetesDriver.namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return driver.OperationResult{}, fmt.Errorf("creating the installer's secret: %w", err)
	}
	defer func() {
		_ = kubernetesDriver.client.CoreV1().Secrets(kubernetesDriver.namespace).Delete(cleanup, secret.Name, metav1.DeleteOptions{})
	}()

	job.Spec.Template.Spec.Volumes[0].Secret.SecretName = secret.Name
	for i := range job.Spec.Template.Spec.Containers[0].Env {
		job.Spec.Template.Spec.Containers[0].Env[i].ValueFrom.SecretKeyRef.Name = secret.Name
	}
	job, err = kubernetesDriver.client.BatchV1().Jobs(kubernetesDriver.namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return driver.OperationResult{}, fmt.Errorf("creating the installer's job: %w", err)
	}
	defer func() {
		_ = kubernetesDriver.client.BatchV1().Jobs(kubernetesDriver.namespace).Delete(cleanup, job.Name, metav1.DeleteOptions{PropagationPolicy: &background})
	}()

	return driver.OperationResult{}, kubernetesDriver.follow(ctx, job.Name, operation.Out)
}

// objects returns the Secret that holds the operation's environment and
// files, and the Job that runs the invocation image with them, each file
// mounted at its path. Run names the Secret once it exists.
func (kubernetesDriver *kubernetesDriver) objects(operation *driver.Operation) (*corev1.Secret, *batchv1.Job) {
	environment := maps.Clone(operation.Environment)
	if environment == nil {
		environment = map[string]string{}
	}
	maps.Copy(environment, kubernetesDriver.environment)
	files := maps.Clone(operation.Files)
	if files == nil {
		files = map[string]string{}
	}
	maps.Copy(files, kubernetesDriver.files)

	metadata := metav1.ObjectMeta{
		GenerateName: generateName(operation),
		Namespace:    kubernetesDriver.namespace,
		Labels:       map[string]string{"app.kubernetes.io/managed-by": "nemo"},
	}
	secret := &corev1.Secret{ObjectMeta: metadata, StringData: map[string]string{}}
	container := corev1.Container{
		Name:            "invocation",
		Image:           imageOf(operation),
		Command:         []string{"/cnab/app/run"},
		ImagePullPolicy: corev1.PullIfNotPresent,
	}
	for _, name := range slices.Sorted(maps.Keys(environment)) {
		key := "env." + name
		secret.StringData[key] = environment[name]
		container.Env = append(container.Env, corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{Key: key}}})
	}
	volume := corev1.Volume{Name: "operation", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{}}}
	for i, path := range slices.Sorted(maps.Keys(files)) {
		key := fmt.Sprintf("file.%d", i)
		secret.StringData[key] = files[path]
		volume.Secret.Items = append(volume.Secret.Items, corev1.KeyToPath{Key: key, Path: key})
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{Name: volume.Name, MountPath: path, SubPath: key, ReadOnly: true})
	}

	noRetry, noToken := int32(0), false
	job := &batchv1.Job{
		ObjectMeta: metadata,
		Spec: batchv1.JobSpec{
			BackoffLimit: &noRetry,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: metadata.Labels},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: &noToken,
					Containers:                   []corev1.Container{container},
					Volumes:                      []corev1.Volume{volume},
				},
			},
		},
	}
	return secret, job
}

// follow streams the logs of the Job's pod once it runs, then waits for the
// Job to succeed or fail.
func (kubernetesDriver *kubernetesDriver) follow(ctx context.Context, jobName string, output io.Writer) error {
	podName, err := kubernetesDriver.started(ctx, jobName)
	if err != nil {
		return err
	}
	if err := kubernetesDriver.stream(ctx, podName, output); err != nil {
		return fmt.Errorf("reading the installer's output: %w", err)
	}
	return kubernetesDriver.finished(ctx, jobName)
}

// started waits for the Job's pod to leave Pending, and returns its name. A
// pod that can't pull its image fails the Job.
func (kubernetesDriver *kubernetesDriver) started(ctx context.Context, jobName string) (string, error) {
	pods := kubernetesDriver.client.CoreV1().Pods(kubernetesDriver.namespace)
	for {
		list, err := pods.List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + jobName})
		if err != nil {
			return "", err
		}
		for _, pod := range list.Items {
			if err := pullFailure(pod); err != nil {
				return "", err
			}
			if pod.Status.Phase != corev1.PodPending {
				return pod.Name, nil
			}
		}
		if err := sleep(ctx); err != nil {
			return "", err
		}
	}
}

var pullFailures = []string{"ErrImagePull", "ImagePullBackOff", "InvalidImageName"}

// pullFailure is why the cluster can't pull the pod's image, when it can't.
func pullFailure(pod corev1.Pod) error {
	for _, status := range pod.Status.ContainerStatuses {
		if waiting := status.State.Waiting; waiting != nil && slices.Contains(pullFailures, waiting.Reason) {
			return fmt.Errorf("the cluster can't pull the installer's image %s: %s", status.Image, waiting.Message)
		}
	}
	return nil
}

// stream copies the installer's output until the pod ends.
func (kubernetesDriver *kubernetesDriver) stream(ctx context.Context, podName string, output io.Writer) error {
	pods := kubernetesDriver.client.CoreV1().Pods(kubernetesDriver.namespace)
	logs, err := pods.GetLogs(podName, &corev1.PodLogOptions{Container: "invocation", Follow: true}).Stream(ctx)
	if err != nil {
		return err
	}
	defer logs.Close()
	_, err = io.Copy(output, logs)
	return err
}

// finished waits for the Job to succeed or fail.
func (kubernetesDriver *kubernetesDriver) finished(ctx context.Context, jobName string) error {
	jobs := kubernetesDriver.client.BatchV1().Jobs(kubernetesDriver.namespace)
	for {
		job, err := jobs.Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		switch {
		case job.Status.Succeeded > 0:
			return nil
		case job.Status.Failed > 0:
			return errors.New("the installer failed")
		}
		if err := sleep(ctx); err != nil {
			return err
		}
	}
}

func sleep(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
		return nil
	}
}

// imageOf is the invocation image, pinned to its digest when the bundle gives one.
func imageOf(operation *driver.Operation) string {
	image := operation.Image.Image
	if operation.Image.Digest != "" && !strings.Contains(image, "@") {
		image += "@" + operation.Image.Digest
	}
	return image
}

var notName = regexp.MustCompile(`[^a-z0-9-]+`)

// generateName prefixes the objects of an operation: nemo-<installation>-<action>-.
func generateName(operation *driver.Operation) string {
	name := notName.ReplaceAllString(strings.ToLower("nemo-"+operation.Installation+"-"+operation.Action), "-")
	if len(name) > 52 {
		name = name[:52]
	}
	return strings.Trim(name, "-") + "-"
}
