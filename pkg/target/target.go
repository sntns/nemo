// Package target describes where a bundle is deployed, and where its
// installer runs. The CLI, the stack file and the Terraform provider fill
// these same types.
package target

import (
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// Orchestrators of a target.
const (
	Compose = "compose"
	Swarm   = "swarm"
)

// How the images reach the target.
const (
	// ImagesRegistry lets the target pull them from their registry. It is the default.
	ImagesRegistry = "registry"
	// ImagesLoad loads them into the target's daemon, with no registry: for
	// Compose, or a swarm of one node, which alone has them.
	ImagesLoad = "load"
)

// Drivers of the installer.
const (
	// DriverDocker runs the invocation image on the target itself, next to
	// what it deploys, with the target's socket mounted. It is the default.
	DriverDocker = "docker"
	// DriverKubernetes runs the invocation image as a Job in a cluster,
	// which reaches the target at its address.
	DriverKubernetes = "kubernetes"
)

// Target is what receives the deployment: the orchestrator that deploys
// there, and its configuration, the same for both orchestrators.
type Target struct {
	// Orchestrator deploys the bundle on the target: compose or swarm.
	Orchestrator  string `json:"orchestrator"`
	Configuration `json:"configuration,omitzero"`
}

// Configuration is what both orchestrators deploy with: one Docker, whose
// address is the only Docker address nemo knows, as whatever must reach
// the target goes through it; its volume types; and how images reach it.
type Configuration struct {
	// Host is the address of the target's Docker: unix://, tcp:// or
	// ssh://user@host. With neither Host nor Context, it is the
	// environment's: DOCKER_HOST, the current docker context, or the local
	// socket.
	Host string `json:"host,omitempty"`
	// Context names a docker context to reach the target with, instead of Host.
	Context string `json:"context,omitempty"`
	// Volumes are the volume types of the target, by name: what a volume of
	// a bundle with that x-nemo-volume type is made of here.
	Volumes map[string]VolumeType `json:"volumes,omitempty"`
	// Images says how the images reach the target: registry, the default,
	// or load.
	Images string `json:"images,omitempty"`
}

// VolumeDriver is a volume driver and its options.
type VolumeDriver struct {
	Driver  string            `json:"driver,omitempty"`
	Options map[string]string `json:"options,omitempty"`
}

// VolumeType is what the volumes of a type are made of on a target. Its
// driver, options and provisioned directory are templates of the volume:
// {{.Stack}}, {{.Name}}, the volume's name in its bundle, and
// {{.Options}}, the options its x-nemo-volume gives, with
// {{AppendJoin .Options ","}} to append them to a list.
type VolumeType struct {
	VolumeDriver
	// Provision creates a directory for each volume before the volume, for
	// drivers that mount a directory of a storage and need it to exist.
	Provision *VolumeProvision `json:"provision,omitempty"`
}

// VolumeProvision creates Directory in the storage that Volume mounts.
type VolumeProvision struct {
	Directory string       `json:"directory"`
	Volume    VolumeDriver `json:"volume"`
}

// Validate checks the target. Its configuration is a field of its own,
// which ozzo would merge into the target's errors as an embedded struct.
func (target Target) Validate() error {
	return validation.Errors{
		"orchestrator":  validation.Validate(target.Orchestrator, validation.Required, validation.In(Compose, Swarm).Error("must be compose or swarm")),
		"configuration": target.Configuration.Validate(),
	}.Filter()
}

// Validate checks the configuration, and each of its volume types.
func (configuration Configuration) Validate() error {
	return validation.ValidateStruct(&configuration,
		validation.Field(&configuration.Host, validation.When(configuration.Context != "", validation.Empty.Error("give a host or a docker context, not both"))),
		validation.Field(&configuration.Volumes),
		validation.Field(&configuration.Images, validation.In(ImagesRegistry, ImagesLoad).Error("must be registry or load")),
	)
}

// Validate checks the volume type's provision, when it has one.
func (volumeType VolumeType) Validate() error {
	return validation.ValidateStruct(&volumeType, validation.Field(&volumeType.Provision))
}

// Validate checks that the provision names its directory.
func (provision VolumeProvision) Validate() error {
	return validation.ValidateStruct(&provision, validation.Field(&provision.Directory, validation.Required))
}

// Installer is where the invocation image runs.
type Installer struct {
	// Driver is docker, the default, or kubernetes.
	Driver string `json:"driver,omitempty"`
	// Kubernetes configures the kubernetes driver.
	Kubernetes *Kubernetes `json:"kubernetes,omitempty"`
}

// Kubernetes is where the kubernetes driver runs the Job. It concerns the
// cluster only: the Job reaches the target at the target's address.
type Kubernetes struct {
	// Namespace is the namespace of the Job.
	Namespace string `json:"namespace"`
	// Kubeconfig is the path of the kubeconfig. Empty, it is the default
	// one (KUBECONFIG, ~/.kube/config), else the cluster nemo runs in.
	Kubeconfig string `json:"kubeconfig,omitempty"`
}

// DriverName returns the driver, docker when none is set.
func (installer Installer) DriverName() string {
	if installer.Driver == "" {
		return DriverDocker
	}
	return installer.Driver
}

// Validate checks the installer: the kubernetes settings go with the
// kubernetes driver, and only with it.
func (installer Installer) Validate() error {
	return validation.ValidateStruct(&installer,
		validation.Field(&installer.Driver, validation.In(DriverDocker, DriverKubernetes).Error("must be docker or kubernetes")),
		validation.Field(&installer.Kubernetes, validation.When(installer.DriverName() == DriverKubernetes,
			validation.Required.Error("the kubernetes driver needs them"),
		).Else(validation.Nil.Error("only the kubernetes driver takes them"))),
	)
}

// Validate checks that the Job has a namespace.
func (kubernetes Kubernetes) Validate() error {
	return validation.ValidateStruct(&kubernetes, validation.Field(&kubernetes.Namespace, validation.Required))
}
