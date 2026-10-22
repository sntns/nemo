// Package convert turns a compose project into the specs of Swarm objects,
// the way docker stack deploy does (docker/cli, cli/compose/convert). It
// only computes: creating the objects is the orchestrator's job.
package convert
