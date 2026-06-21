// Package config loads the agent runtime configuration from environment
// variables (with an optional flag override for the config file path).
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// Defaults applied when the backend does not specify otherwise.
const (
	DefaultPollIntervalSeconds = 60
)

// Config is the agent's own runtime configuration (how to reach the backend),
// distinct from the list of checks it pulls from that backend.
type Config struct {
	// BackendURL is the base URL of the control-plane, e.g.
	// https://status.aldrys.com. The agent appends /api/agent/... to it.
	BackendURL string

	// Token authenticates the agent to the backend (Bearer). Scoped to the
	// tenant in cloud mode.
	Token string

	// ChecksFile, when set, makes the agent load its checks from a local JSON
	// file (a ConfigResponse) instead of pulling them from the backend. Results
	// are still pushed to the backend unless it is unset. Useful for the
	// simplest self-host case and for offline testing.
	ChecksFile string

	// PollIntervalSeconds is the fallback config-refresh interval used until the
	// backend advertises its own value.
	PollIntervalSeconds int
}

// Load reads the configuration from the environment. ChecksFileOverride, when
// non-empty, takes precedence over the RESPONDING_CHECKS_FILE variable.
func Load(checksFileOverride string) (Config, error) {
	c := Config{
		BackendURL:          strings.TrimRight(os.Getenv("RESPONDING_BACKEND_URL"), "/"),
		Token:               os.Getenv("RESPONDING_AGENT_TOKEN"),
		ChecksFile:          os.Getenv("RESPONDING_CHECKS_FILE"),
		PollIntervalSeconds: DefaultPollIntervalSeconds,
	}
	if checksFileOverride != "" {
		c.ChecksFile = checksFileOverride
	}
	if v := os.Getenv("RESPONDING_POLL_INTERVAL"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, errors.New("RESPONDING_POLL_INTERVAL must be a positive integer (seconds)")
		}
		c.PollIntervalSeconds = n
	}
	return c, c.Validate()
}

// Validate ensures the configuration can actually drive the agent.
func (c Config) Validate() error {
	// Local-file mode without a backend is valid for dry runs: checks are read
	// from disk and results are logged but not dispatched.
	if c.BackendURL == "" && c.ChecksFile == "" {
		return errors.New("set RESPONDING_BACKEND_URL (to pull config) or RESPONDING_CHECKS_FILE (local checks)")
	}
	if c.BackendURL != "" && c.Token == "" && c.ChecksFile == "" {
		return errors.New("RESPONDING_AGENT_TOKEN is required when pulling config from RESPONDING_BACKEND_URL")
	}
	return nil
}

// DispatchEnabled reports whether results should be pushed to a backend.
func (c Config) DispatchEnabled() bool {
	return c.BackendURL != "" && c.Token != ""
}
