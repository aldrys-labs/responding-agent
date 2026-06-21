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

## Status

Early development. The agent runs the full check loop (pull config, run checks,
push results, heartbeat) against a backend or a local checks file.

## Quick start

```sh
# Against a backend (pulls its check list, pushes results):
export RESPONDING_BACKEND_URL=https://status.example.com
export RESPONDING_AGENT_TOKEN=your-agent-token
responding-agent

# Or with a local checks file, no backend required (dry run):
responding-agent -checks ./checks.json
```

See [docs/configuration](#configuration) below.

## Configuration

The agent is configured through environment variables:

| Variable | Required | Description |
|---|---|---|
| `RESPONDING_BACKEND_URL` | yes* | Backend base URL. The agent calls `<url>/api/agent/...`. |
| `RESPONDING_AGENT_TOKEN` | yes* | Bearer token used to authenticate the agent. |
| `RESPONDING_CHECKS_FILE` | no | Path to a local checks JSON file (overrides pulling config). |
| `RESPONDING_POLL_INTERVAL` | no | Config-refresh interval in seconds (default 60). |

\* Either a backend URL + token, or a local checks file, must be provided.

The `-checks <path>` flag overrides `RESPONDING_CHECKS_FILE`.

### Local checks file

```json
{
  "pollIntervalSeconds": 60,
  "checks": [
    { "id": "web", "type": "http", "target": "https://example.com", "intervalSeconds": 30, "timeoutMs": 5000, "expectedStatus": 200 },
    { "id": "db",  "type": "tcp",  "target": "10.0.0.5:5432", "intervalSeconds": 30, "timeoutMs": 3000 },
    { "id": "cert","type": "tls",  "target": "example.com:443", "intervalSeconds": 3600, "timeoutMs": 5000, "tlsExpiryWarningDays": 14 }
  ]
}
```

## Build

```sh
go build ./cmd/responding-agent
go test ./...
```

## License

MIT. See [LICENSE](./LICENSE).
