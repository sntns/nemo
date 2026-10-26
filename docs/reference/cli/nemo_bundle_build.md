## nemo bundle build

Build a bundle from compose files with x-nemo-bundle

```
nemo bundle build [flags]
```

### Examples

```
  nemo bundle build -f compose.yaml --image nginx:1.27-alpine=docker://nginx:1.27-alpine
  nemo bundle build -f compose.yaml --version 1.4.0 \
    --image shop-api:latest=oci:build/images/shop-api.oci \
    -o docker://registry.example.com/shop/api
```

### Options

```
  -f, --file stringArray    compose file, merged in order; one has x-nemo-bundle (repeatable)
  -h, --help                help for build
      --image stringArray   where an image of the compose is built, NAME=docker://REFERENCE or NAME=oci:PATH (repeatable)
      --installer string    the invocation image, gill: docker://REFERENCE or oci:PATH to carry it with the bundle, or an image name (NEMO_INSTALLER, else the gill of this nemo's version)
  -o, --output string       a bundle.json to write, or where to push the bundle and its images: docker://REFERENCE or oci:PATH (default "bundle.json")
      --version string      version of the bundle, over x-nemo-bundle's
```

### SEE ALSO

* [nemo bundle](nemo_bundle.md)	 - Build and copy bundles

