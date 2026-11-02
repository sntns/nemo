package cnab

// Parameters a nemo bundle declares, so that a runtime, nemo's or Porter,
// delivers them to the installer like any CNAB parameter.
const (
	// ParameterOrchestrator is the orchestrator of the target: compose or
	// swarm. It is required: the installer never guesses it.
	ParameterOrchestrator = "nemo-orchestrator"
	// ParameterTimeout bounds the action, as a Go duration.
	ParameterTimeout = "nemo-timeout"
	// ParameterPurge also removes the volumes on uninstall.
	ParameterPurge = "nemo-purge"
	// ParameterVolumes holds the volume types of the target, as JSON.
	ParameterVolumes = "nemo-volumes"
	// ParameterRegistryAuth holds the credentials of the registries the
	// images are pulled from, by domain, as JSON. It is sensitive: the
	// runtime fills it from docker login and never stores it.
	ParameterRegistryAuth = "nemo-registry-auth"
	// ParameterOverride is a compose document, as YAML, that the installer
	// fuses into the bundle's, last: the override a stack gives an installation.
	ParameterOverride = "nemo-override"
)

// CredentialOverride holds the secrets an override reads its secrets' and
// configs' contents from, as JSON by variable. The runtime fills it from the
// credentials the stack gives that the bundle does not declare.
const CredentialOverride = "nemo-credentials"
