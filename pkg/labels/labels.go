// Package labels holds the labels nemo puts on everything the installer
// creates. The installer tells its own objects from others by them.
package labels

import "maps"

const (
	// Stack is the stack the installation belongs to.
	Stack = "io.nemo.stack"
	// Bundle is the name of the installed bundle.
	Bundle = "io.nemo.bundle"
	// Version is the version of the installed bundle.
	Version = "io.nemo.version"
	// Orchestrator is the orchestrator that created the object: compose or swarm.
	Orchestrator = "io.nemo.orchestrator"
	// Revision is the CNAB revision of the action that last changed the object.
	Revision = "io.nemo.revision"
	// Service is the compose service a container or a Swarm service runs.
	Service = "io.nemo.service"
	// Hash identifies the spec an object was made from: it is replaced when the hash changes.
	Hash = "io.nemo.hash"
	// Config is the compose key of a Swarm config.
	Config = "io.nemo.config"
	// Secret is the compose key of a Swarm secret.
	Secret = "io.nemo.secret"
)

// Merge returns the union of the label sets; a later set wins.
func Merge(sets ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, set := range sets {
		maps.Copy(merged, set)
	}
	return merged
}
