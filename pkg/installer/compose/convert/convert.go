// Package convert turns a compose project into what the Compose orchestrator
// creates on the Docker Engine: the create options of each container, its
// networks and volumes, and the state to wait for, the way docker compose
// does. It only computes: creating the objects is the orchestrator's job.
package convert
