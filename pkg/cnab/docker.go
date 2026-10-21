package cnab

// DockerExtension gives the invocation image access to the host's Docker
// daemon. Every nemo bundle requires it: a runtime that honours it mounts
// the Docker socket into the installer, and Porter asks for
// --allow-docker-host-access before it does.
var DockerExtension = Extension[Docker]{
	Key:       OfficialExtensionsPrefix + "docker",
	Shorthand: "docker",
	Schema:    "io.cnab.docker.schema.json",
}

// Docker is the configuration of the Docker extension.
type Docker struct {
	// Privileged represents whether or not the Docker container should run as --privileged
	Privileged bool `json:"privileged,omitempty"`
}
