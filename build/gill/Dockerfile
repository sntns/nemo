# syntax=docker/dockerfile:1

# gill, the invocation image of every nemo bundle. Built from the root of the
# repository: docker build -f build/gill/Dockerfile .
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/run ./cmd/gill

# Root, so that it can use the Docker socket the runtime mounts.
FROM gcr.io/distroless/static-debian12
LABEL org.opencontainers.image.title="gill" \
      org.opencontainers.image.description="The invocation image of nemo bundles: deploys their compose project with Compose or Swarm" \
      org.opencontainers.image.source="https://github.com/sntns/nemo" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/run /cnab/app/run
CMD ["/cnab/app/run"]
