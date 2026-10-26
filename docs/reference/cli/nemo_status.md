## nemo status

Show where a stack stands: its installations, and the services they run

```
nemo status [flags]
```

### Examples

```
  nemo status                   # the current stack
  nemo status --stack demo --all
```

### Options

```
  -a, --all            show the services of every installation, not only of those not ready
  -h, --help           help for status
      --json           print the status as JSON on stdout
      --stack string   name of the stack (default: the current one)
```

### SEE ALSO

* [nemo](nemo.md)	 - Deploy CNAB bundles to Docker, with Compose or Swarm

