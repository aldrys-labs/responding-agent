# Changelog

## 0.1.0 (2026-06-21)

Initial development release (pre-1.0). Versions stay in the 0.x range until the
agent/backend protocol is frozen and dogfooded.

### Features

* Go monitoring agent running HTTP, TCP, TLS-expiry and ICMP-ping checks from
  inside the customer network, classifying up / degraded / down with latency.
* Outbound dispatch to the backend: pull config, push result batches, send
  heartbeats, authenticated with a Bearer token. No inbound port.
* Resilience: result spool with disk persistence and replay (survives backend
  outage and agent restart), and a config cache for resilient startup.
* Observability: local `/healthz`, `/readyz` and `/metrics` (Prometheus)
  endpoints, plus a `healthcheck` subcommand for the container HEALTHCHECK.
* Operability: token-from-file, per-check insecure TLS for internal self-signed
  services, panic recovery, scheduling jitter, concurrency cap, SIGHUP reload.
* Packaging: static multi-arch binaries (linux/darwin/windows, amd64/arm64),
  distroless Docker image, and a one-line installer.
