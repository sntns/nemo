## nemo installation upgrade

Upgrade an installation, to a new bundle or its own

```
nemo installation upgrade [INSTALLATION] [flags]
```

### Examples

```
  nemo installation upgrade nginx --param region=eu6
  nemo installation upgrade -f nginx-1.1.0/bundle.json
```

### Options

```
      --context string                docker context to reach the target with, instead of --host
      --credential stringArray        credential source, NAME=KEY:VALUE with KEY a CNAB source, path, env, command or value, over the installation's (repeatable)
      --driver string                 where the invocation image runs: docker, on the target; or kubernetes, as a Job in a cluster that reaches the target over tcp:// (default: the installation's, else docker)
  -f, --file string                   the bundle: a bundle.json, or where it was pushed, docker://REFERENCE or oci:PATH (default: the installation's)
      --force                         write a stack another tool manages, Terraform: its next run undoes it
  -h, --help                          help for upgrade
      --host string                   address of the target's Docker: unix://, tcp:// or ssh://user@host (default: the installation's, else the environment's)
      --images string                 how the images reach the target: registry, which the target pulls from, or load, into its daemon with no registry (default: the installation's, else registry)
      --json                          print the record of the run as JSON on stdout
      --kubeconfig string             kubeconfig of the cluster, with --driver kubernetes (default: KUBECONFIG, ~/.kube/config, else the cluster nemo runs in)
      --kubernetes-namespace string   namespace of the Job, with --driver kubernetes
      --orchestrator string           how the installer deploys the bundle on the target: compose or swarm (default: the installation's)
      --param stringArray             parameter value, NAME=VALUE, over the installation's (repeatable)
      --stack string                  stack the installation belongs to (default: the current one)
      --volumes string                JSON file of the target's volume types, which make the volumes of a bundle with an x-nemo-volume type (default: the installation's)
```

### SEE ALSO

* [nemo installation](nemo_installation.md)	 - What nemo remembers of an installation, and the CNAB actions on one, without a manifest

