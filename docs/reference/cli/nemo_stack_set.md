## nemo stack set

Change a stack, the current one by default

```
nemo stack set [NAME] [flags]
```

### Examples

```
  nemo stack set --remote "oci:$PWD/build/bundles/{{ .Component }}.oci" --param region=eu6 --credential token=env:TOKEN
```

### Options

```
      --credential stringArray   credential of the stack, NAME=KEY:VALUE with KEY a CNAB source, path, env, command or value (repeatable)
  -h, --help                     help for set
      --param stringArray        parameter of the stack, NAME=VALUE (repeatable)
      --remote string            where the bundles given by version are: docker://…/{{ .Component }}:{{ .Version }} or oci:…/{{ .Component }}.oci
```

### SEE ALSO

* [nemo stack](nemo_stack.md)	 - Keep stacks: their manifests, in nemo's home, and the current one

