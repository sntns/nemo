# Comparison

nemo sits between the tools that run containers on Docker and the tools that orchestrate deployments. This page says where it overlaps with tools you may already use, and where it differs.

## Docker Compose

`docker compose up` runs one project on one Docker, from files on the machine that runs it.

nemo keeps the compose format and adds what a deployment needs around it: each project becomes a versioned bundle with its images pinned by digest; several bundles deploy together in dependency order; the target can be remote, a swarm, or offline; settings and secrets are declared and given per environment; and every action is recorded. Containers created by nemo on a Compose target are labeled as Compose labels them, so `docker compose ps` and `docker compose logs` keep working.

Use Docker Compose alone for one project you run by hand. Use nemo when the same projects must be deployed, versioned and tracked across several environments.

## Docker Swarm stacks

`docker stack deploy` applies one compose file to a swarm, and returns as soon as the services are created.

nemo deploys the same files to a swarm, and also orders several stacks by their dependencies, waits until each service's tasks run and stay up before going on, versions configs and secrets by their content so that changes roll the services, pins images by digest so that every node runs the same one, and records each deployment. The same bundles also run on Compose targets, without Swarm.

## Porter

[Porter](https://porter.sh) is a general CNAB tool: bundles describe their installation with `porter.yaml` and mixins (Helm, Terraform, cloud CLIs, scripts), for any kind of target.

nemo is specialised for applications made of containers on Docker. Its bundles are made from the compose files you already have, with no installer to write: gill deploys them with Compose or Swarm. On top of CNAB, it adds stacks of bundles on one target, image loading without a registry, volume types per target, live progress, status and logs. Both follow CNAB; nemo's bundles are designed to run with other CNAB runtimes too.

## Helm

[Helm](https://helm.sh) packages and deploys applications on Kubernetes: charts, values, releases and their history.

nemo brings the same ideas to Docker: bundles are versioned packages like charts, parameters and credentials are values, installations and their runs are releases and their history. It targets Docker Engine, on one machine or a swarm, where running Kubernetes would be too much: edge devices, customer servers, small clusters, developer machines.

## Kamal

[Kamal](https://kamal-deploy.org) deploys web applications to servers over SSH with Docker, with zero-downtime switches through its proxy.

nemo deploys platforms made of many services, described by compose files, with their dependencies, volumes, configs and secrets, on Compose or Swarm, and works without network on site. Kamal focuses on one web application and its accessories, with a convention-driven configuration.

## Scripts and configuration management

Shell scripts, Makefiles or Ansible playbooks that run `docker compose` or `docker stack deploy` on each server can do anything, and must do everything: ordering, waiting, passing secrets, cleaning up removed services, remembering versions.

nemo replaces that glue with a declarative manifest and a single command that brings each target to it. Configuration management tools remain useful to prepare the hosts: install Docker, users, firewalls, storage.

## Terraform

[Terraform](https://www.terraform.io) provisions infrastructure: machines, networks, DNS, managed services.

nemo deploys what runs on that infrastructure's Docker. The two work together: Terraform creates the hosts, nemo deploys the application onto them.
