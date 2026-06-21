# Build a static, dependency-free binary, then ship it on a minimal base image.
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/responding-agent ./cmd/responding-agent

# distroless/static is tiny and has no shell; nonroot drops privileges.
# Note: ICMP "ping" checks need NET_RAW (or a permissive ping_group_range).
# Run with --cap-add=NET_RAW if you use ping checks; HTTP/TCP/TLS need nothing.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/responding-agent /usr/local/bin/responding-agent

# Enable the health/metrics server by default so the container HEALTHCHECK works
# out of the box. Override or unset RESPONDING_HEALTH_ADDR to change or disable.
ENV RESPONDING_HEALTH_ADDR=:9090
EXPOSE 9090

# The healthcheck subcommand probes /healthz over the loopback (no shell needed).
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/responding-agent", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/responding-agent"]
