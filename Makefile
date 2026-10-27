# Stamped into nemo, which then defaults its bundles' installer to the gill of
# that version: make build VERSION=0.1.0.
VERSION ?=
GILL ?= gill:dev

LDFLAGS := -s -w $(if $(VERSION),-X main.stamped=$(VERSION))

.PHONY: all build install test integration lint gill gill-oci reference clean help

all: lint test build ## Lint, test and build

build: ## Build nemo into bin/
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/nemo ./cmd/nemo

install: ## Install nemo into GOBIN
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/nemo

test: ## Run the tests
	go test -race ./...

integration: ## Run the integration bench on the Docker of DOCKER_HOST, a swarm manager: it creates and removes objects there
	go test -tags integration -count=1 -timeout 30m ./test/integration/...

lint: ## Run golangci-lint
	golangci-lint run ./...

gill: ## Build gill's image into the local Docker, as $(GILL)
	docker build -f build/gill/Dockerfile -t $(GILL) .

gill-oci: ## Build gill as an OCI layout, for NEMO_INSTALLER=oci:$(CURDIR)/bin/gill.oci
	rm -rf bin/gill.oci
	docker buildx build -f build/gill/Dockerfile --provenance=false --output type=oci,dest=bin/gill.oci,tar=false .

reference: ## Write the reference of the commands into docs/reference/cli
	rm -rf docs/reference/cli && mkdir -p docs/reference/cli
	NEMO_INSTALLER= go run ./cmd/nemo reference docs/reference/cli

clean: ## Remove what the build wrote
	rm -rf bin dist

help: ## List the targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'
