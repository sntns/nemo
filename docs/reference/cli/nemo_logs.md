## nemo logs

Print the logs of the services of a stack, of one installation, or of one service

```
nemo logs [INSTALLATION [SERVICE]] [flags]
```

### Examples

```
  nemo logs -f
  nemo logs api --tail 100
  nemo logs database postgres -f
```

### Options

```
  -f, --follow         follow the logs, until interrupted
  -h, --help           help for logs
      --stack string   name of the stack (default: the current one)
  -n, --tail string    how many lines of each service to start with: a number, or all (default "all")
```

### SEE ALSO

* [nemo](nemo.md)	 - Deploy CNAB bundles to Docker, with Compose or Swarm

