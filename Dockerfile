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
ENTRYPOINT ["/usr/local/bin/responding-agent"]
