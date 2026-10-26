# Examples

## hello

Two bundles deployed together with Compose on your local Docker, with no registry:

| | |
|---|---|
| [`hello/nginx/compose.yaml`](hello/nginx/compose.yaml) | A web page filled with the `region` parameter, a secret token from the `token` credential, and a cache volume of type `data`. |
| [`hello/whoami/compose.yaml`](hello/whoami/compose.yaml) | A service that answers with its own details, and requires `nginx`. |
| [`hello/stack.yaml`](hello/stack.yaml) | The stack: Compose on the local Docker, images loaded without a registry, the `data` volume type, the region, and the token read from `HELLO_TOKEN`. |

```bash
cd examples/hello
nemo bundle build -f nginx/compose.yaml \
  --image nginx:1.27-alpine=docker://nginx:1.27-alpine -o oci:build/nginx.oci
nemo bundle build -f whoami/compose.yaml \
  --image traefik/whoami:v1.10=docker://traefik/whoami:v1.10 -o oci:build/whoami.oci
nemo stack import stack.yaml
HELLO_TOKEN=s3cret nemo up
curl localhost:8080             # hello from nemo in eu5
curl localhost:8081             # whoami
nemo down --purge
```

The [getting started guide](../docs/getting-started.md) explains each step.
