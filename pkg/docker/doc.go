// Package docker is the installer's client of the Docker Engine for what
// every Docker has: a Client that creates, finds and deletes networks,
// volumes, containers and images from their specs, one file per kind. What
// only a swarm has, services, tasks, configs and secrets, is the swarm
// package's. Around the Client: selecting objects, telling the installer's
// own, recognising the daemon's errors, and the settings both orchestrators
// convert alike.
package docker
