# Changelog

## [0.1.1](https://github.com/aldrys-labs/responding-agent/compare/v0.1.0...v0.1.1) (2026-07-05)


### Bug Fixes

* drop results of checks aborted by a generation swap ([55128b6](https://github.com/aldrys-labs/responding-agent/commit/55128b65441907d8173322166e87717aef3a0c82)), closes [#3](https://github.com/aldrys-labs/responding-agent/issues/3)
* emit result timestamps with sub-second precision ([21af32c](https://github.com/aldrys-labs/responding-agent/commit/21af32c906c1c7ca2c495c1a513299d1e63ad3e4)), closes [#10](https://github.com/aldrys-labs/responding-agent/issues/10)
* evaluate 3xx expectations on the redirect itself and honor a Host header ([b15b2fb](https://github.com/aldrys-labs/responding-agent/commit/b15b2fb1f206dbb9e34dfb5617d4979e305fedce)), closes [#7](https://github.com/aldrys-labs/responding-agent/issues/7)
* keep checks running while the backend hangs ([efa9ddd](https://github.com/aldrys-labs/responding-agent/commit/efa9ddd16cdd0a8b9f7e5cdab5cc9d25389acf2c)), closes [#5](https://github.com/aldrys-labs/responding-agent/issues/5)
* keep the spool count honest when a corrupt batch cannot be removed ([4aa285b](https://github.com/aldrys-labs/responding-agent/commit/4aa285ba31220c0f62f47a0aac706beaf1fbd5a9)), closes [#11](https://github.com/aldrys-labs/responding-agent/issues/11)
* ping IPv6 targets over ICMPv6 ([ae7e09d](https://github.com/aldrys-labs/responding-agent/commit/ae7e09dca97b1d8bd72720db09e7dd3b93632711)), closes [#12](https://github.com/aldrys-labs/responding-agent/issues/12)
* reconcile check loops instead of restarting them on every poll ([c5bef0d](https://github.com/aldrys-labs/responding-agent/commit/c5bef0d9f90745c6731db3c194a936db401fc3e6)), closes [#4](https://github.com/aldrys-labs/responding-agent/issues/4)
* report an expired TLS certificate as down ([fbb0612](https://github.com/aldrys-labs/responding-agent/commit/fbb0612eaef86164409fe5c4def60e21597ad881)), closes [#6](https://github.com/aldrys-labs/responding-agent/issues/6)
* spool the final batch directly on shutdown instead of posting ([b673708](https://github.com/aldrys-labs/responding-agent/commit/b673708baf912d1ce73180d9aec09443d10bc975)), closes [#9](https://github.com/aldrys-labs/responding-agent/issues/9)
* validate BackendURL at startup and refuse cleartext token transport ([d22cddb](https://github.com/aldrys-labs/responding-agent/commit/d22cddb1eac26cf5299889b740c1987cccfd4d67)), closes [#8](https://github.com/aldrys-labs/responding-agent/issues/8)

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
