# syntax=docker/dockerfile:1

# Build stage. TARGETOS/TARGETARCH are supplied by buildx for each platform in
# the manifest list, so the toolchain cross-compiles instead of running under
# QEMU emulation.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /workspace

# Dependencies first, so a source-only change reuses the module cache layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

# -trimpath keeps build paths out of the binary so the output is reproducible.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/webhook .

# Runtime stage.
#
# distroless/static holds a CA bundle, /etc/passwd and tzdata and nothing else:
# no shell, no package manager, no libc. That removes essentially the entire
# CVE surface that a distribution base image carries, which is what made the
# image this project forks from accumulate 5 critical / 38 high findings.
#
# The `nonroot` variant runs as uid 65532. The webhook therefore cannot bind a
# privileged port, so it defaults to 8443 rather than 443 and needs no
# NET_BIND_SERVICE capability.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/webhook /webhook

USER 65532:65532
EXPOSE 8443

ENTRYPOINT ["/webhook"]
CMD ["--secure-port=8443"]

LABEL org.opencontainers.image.title="cert-manager-webhook-porkbun" \
      org.opencontainers.image.description="cert-manager ACME DNS01 solver webhook for Porkbun" \
      org.opencontainers.image.source="https://github.com/octabits-io/cert-manager-webhook-porkbun" \
      org.opencontainers.image.licenses="Apache-2.0"
