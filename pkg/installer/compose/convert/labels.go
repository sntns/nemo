package convert

// Labels docker compose puts on what it creates.
const (
	ProjectLabel    = "com.docker.compose.project"
	ServiceLabel    = "com.docker.compose.service"
	NumberLabel     = "com.docker.compose.container-number"
	OneoffLabel     = "com.docker.compose.oneoff"
	ConfigHashLabel = "com.docker.compose.config-hash"
	NetworkLabel    = "com.docker.compose.network"
	VolumeLabel     = "com.docker.compose.volume"
)
