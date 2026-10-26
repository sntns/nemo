## nemo down

Uninstall every installation of a stack

```
nemo down [flags]
```

### Examples

```
  nemo down                     # the current stack
  nemo down --stack demo --purge
  nemo down -f manifest.yaml
```

### Options

```
  -f, --file string    a stack's manifest
  -h, --help           help for down
      --json           print the record of the runs as JSON on stdout
      --purge          also remove the volumes
      --stack string   name of the stack (default: the current one)
```

### SEE ALSO

* [nemo](nemo.md)	 - Deploy CNAB bundles to Docker, with Compose or Swarm

