// Package protocol defines the wire types exchanged between the agent and the
// respondi.ng backend (control-plane). These types are the contract: config is
// pulled from the backend, results are pushed back. Keep JSON tags stable.
package protocol

// CheckType enumerates the kinds of checks an agent can run.
type CheckType string

const (
	CheckHTTP CheckType = "http"
	CheckTCP  CheckType = "tcp"
	CheckTLS  CheckType = "tls"
	CheckPing CheckType = "ping"
)

// Status is the outcome of a single check run.
type Status string

const (
	StatusUp       Status = "up"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

// Check describes one thing to monitor. The backend distributes a list of these
// to the agent. Fields outside the common set are type-specific and optional.
type Check struct {
	ID              string    `json:"id"`
	Type            CheckType `json:"type"`
	Target          string    `json:"target"`
	IntervalSeconds int       `json:"intervalSeconds"`
	TimeoutMs       int       `json:"timeoutMs"`

	// HTTP-specific.
	Method         string            `json:"method,omitempty"`
	ExpectedStatus int               `json:"expectedStatus,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           string            `json:"body,omitempty"`

	// Optional latency threshold above which a reachable check is reported as
	// degraded rather than up. Zero disables it.
	DegradedAboveMs int `json:"degradedAboveMs,omitempty"`

	// TLS-specific: report degraded when the certificate expires within this
	// many days. Zero disables the warning.
	TLSExpiryWarningDays int `json:"tlsExpiryWarningDays,omitempty"`
}

// ConfigResponse is what GET /api/agent/config returns.
type ConfigResponse struct {
	PollIntervalSeconds int     `json:"pollIntervalSeconds"`
	Checks              []Check `json:"checks"`
}

// Result is one observation, pushed to POST /api/agent/results (batched).
type Result struct {
	CheckID   string `json:"checkId"`
	Timestamp string `json:"ts"` // RFC3339
	Status    Status `json:"status"`
	LatencyMs int64  `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
}

// Heartbeat is pushed to POST /api/agent/heartbeat so the backend knows which
// agents are alive.
type Heartbeat struct {
	AgentVersion string `json:"agentVersion"`
	Hostname     string `json:"hostname"`
	Timestamp    string `json:"ts"` // RFC3339
}
