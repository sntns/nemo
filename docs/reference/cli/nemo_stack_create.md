## nemo stack create

Create a stack in a form: its bundles, its target, and the parameters and credentials they declare

### Synopsis

Create a stack in a form, and make it the current one. The bundles come from a
directory of OCI layouts or from a registry; the form then asks for the
parameters and the credentials they declare, and the volume types they use
get a local volume, to change in the manifest for another driver.

```
nemo stack create [NAME] [flags]
```

### Options

```
  -h, --help   help for create
```

### SEE ALSO

* [nemo stack](nemo_stack.md)	 - Keep stacks: their manifests, in nemo's home, and the current one

