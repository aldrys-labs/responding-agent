# responding-agent

The respondi.ng monitoring agent: a single static Go binary that runs **inside
your network**, executes checks (HTTP / TCP / TLS expiry / ping) against your
internal and public services, and pushes the results outbound to a respondi.ng
backend (self-hosted or cloud). No inbound port to open.

Part of [respondi.ng](https://respondi.ng), an open-source, self-hostable uptime
and status-page monitor by [Aldrys](https://aldrys.com).

## Why an agent

Public monitors (UptimeRobot, BetterStack, Pingdom) cannot reach services behind
your firewall. The agent lives where your services live, so it can watch internal
APIs, databases and private endpoints alongside your public ones, and reports
back over a single outbound connection.

## How it works

The agent pulls its check list from the backend (or reads a local file), runs
each check on its own schedule, and pushes result batches plus periodic
heartbeats back to the backend over HTTPS, authenticated with a Bearer token.
Undelivered results are spooled and replayed, so a backend outage or an agent
restart does not lose data.

## Quick start

```sh
# Against a backend (pulls its check list, pushes results):
export RESPONDING_BACKEND_URL=https://status.example.com
export RESPONDING_AGENT_TOKEN=your-agent-token
responding-agent

# Or with a local checks file, no backend required (dry run):
responding-agent -checks ./checks.json
```

### Docker

```sh
docker run -d --name responding-agent \
  -e RESPONDING_BACKEND_URL=https://status.example.com \
  -e RESPONDING_AGENT_TOKEN=your-agent-token \
  -v responding-spool:/var/spool/responding \
  -e RESPONDING_SPOOL_DIR=/var/spool/responding \
  ghcr.io/aldrys-labs/responding-agent:latest
```

The image enables the health server on `:9090` and ships a `HEALTHCHECK`. Add
`--cap-add=NET_RAW` only if you use ICMP `ping` checks.

## Configuration

The agent is configured through environment variables:

| Variable | Required | Default | Description |
|---|---|---|---|
| `RESPONDING_BACKEND_URL` | yes* | | Backend base URL. The agent calls `<url>/api/agent/...`. |
| `RESPONDING_AGENT_TOKEN` | yes* | | Bearer token used to authenticate the agent. |
| `RESPONDING_AGENT_TOKEN_FILE` | no | | Path to read the token from (Docker/Kubernetes secret). Overrides the inline token. |
| `RESPONDING_CHECKS_FILE` | no | | Path to a local checks JSON file. Overrides pulling config from the backend. |
| `RESPONDING_POLL_INTERVAL` | no | `60` | Config-refresh interval in seconds. |
| `RESPONDING_SPOOL_DIR` | no | | Directory for the result spool and config cache. Persists across restarts. When unset, results are buffered in memory only. |
| `RESPONDING_SPOOL_MAX_BATCHES` | no | `1000` | Spool cap; oldest batches are dropped when exceeded. |
| `RESPONDING_HEALTH_ADDR` | no | | Address (e.g. `:9090`) for the local health/metrics server. Disabled when unset (set by default in the Docker image). |
| `RESPONDING_MAX_CONCURRENT_CHECKS` | no | `64` | Maximum checks running at the same time. |

\* Either a backend URL + token, or a local checks file, must be provided.

The `-checks <path>` flag overrides `RESPONDING_CHECKS_FILE`. Send `SIGHUP` to
reload the configuration without restarting.

### Local checks file

```json
{
  "pollIntervalSeconds": 60,
  "checks": [
    { "id": "web", "type": "http", "target": "https://example.com", "intervalSeconds": 30, "timeoutMs": 5000, "expectedStatus": 200 },
    { "id": "internal-api", "type": "http", "target": "https://10.0.0.5", "intervalSeconds": 30, "timeoutMs": 5000, "insecureSkipVerify": true },
    { "id": "db",  "type": "tcp",  "target": "10.0.0.5:5432", "intervalSeconds": 30, "timeoutMs": 3000 },
    { "id": "cert","type": "tls",  "target": "example.com:443", "intervalSeconds": 3600, "timeoutMs": 5000, "tlsExpiryWarningDays": 14 },
    { "id": "gw",  "type": "ping", "target": "10.0.0.1", "intervalSeconds": 30, "timeoutMs": 2000 }
  ]
}
```

Check fields: `id`, `type` (`http`/`tcp`/`tls`/`ping`), `target`,
`intervalSeconds`, `timeoutMs`, and per type: `method`, `expectedStatus`,
`headers`, `body`, `degradedAboveMs`, `insecureSkipVerify` (http/tls),
`tlsExpiryWarningDays` (tls).

## Observability

When `RESPONDING_HEALTH_ADDR` is set, the agent serves:

- `GET /healthz` - liveness, always `200` while running.
- `GET /readyz` - `200` once a configuration has been applied, else `503`.
- `GET /metrics` - Prometheus metrics (checks run/up/down, results posted,
  spool depth, dispatch failures, heartbeats, ...).

`responding-agent healthcheck` probes `/healthz` and exits 0/1; it backs the
container `HEALTHCHECK`.

## Notes

- **ICMP ping** needs an unprivileged ICMP socket: works without root on macOS,
  and on Linux when `net.ipv4.ping_group_range` permits it. In Docker, run with
  `--cap-add=NET_RAW`. HTTP, TCP and TLS checks need no special privileges.
- **Self-signed internal services**: set `insecureSkipVerify` on the check.
- **Data durability**: set `RESPONDING_SPOOL_DIR` to a persistent volume so
  results survive a backend outage and agent restarts.

## Build

```sh
go build ./cmd/responding-agent
go test ./...
```

## License

MIT. See [LICENSE](./LICENSE).
