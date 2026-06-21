// Package config loads the agent runtime configuration from environment
// variables (with an optional flag override for the config file path).
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Defaults applied when the backend does not specify otherwise.
const (
	DefaultPollIntervalSeconds = 60
	DefaultSpoolMaxBatches     = 1000
	DefaultMaxConcurrentChecks = 64
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

	// SpoolDir is where undelivered result batches and the last-good config are
	// persisted. When empty, results are buffered in memory only (lost on
	// restart) and the config is not cached.
	SpoolDir string

	// SpoolMaxBatches caps how many result batches are kept before the oldest
	// are dropped.
	SpoolMaxBatches int

	// HealthAddr, when set (e.g. ":9090"), enables the local health and metrics
	// HTTP server.
	HealthAddr string

	// MaxConcurrentChecks bounds how many checks may run at the same time.
	MaxConcurrentChecks int
}

// Load reads the configuration from the environment. ChecksFileOverride, when
// non-empty, takes precedence over the RESPONDING_CHECKS_FILE variable.
func Load(checksFileOverride string) (Config, error) {
	c := Config{
		BackendURL:          strings.TrimRight(os.Getenv("RESPONDING_BACKEND_URL"), "/"),
		Token:               os.Getenv("RESPONDING_AGENT_TOKEN"),
		ChecksFile:          os.Getenv("RESPONDING_CHECKS_FILE"),
		PollIntervalSeconds: DefaultPollIntervalSeconds,
		SpoolDir:            os.Getenv("RESPONDING_SPOOL_DIR"),
		SpoolMaxBatches:     DefaultSpoolMaxBatches,
		HealthAddr:          os.Getenv("RESPONDING_HEALTH_ADDR"),
		MaxConcurrentChecks: DefaultMaxConcurrentChecks,
	}
	if checksFileOverride != "" {
		c.ChecksFile = checksFileOverride
	}

	// A token file (Docker/Kubernetes secret) takes precedence over the inline
	// token so the secret never has to live in the process environment.
	if path := os.Getenv("RESPONDING_AGENT_TOKEN_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read RESPONDING_AGENT_TOKEN_FILE: %w", err)
		}
		c.Token = strings.TrimSpace(string(data))
	}

	if err := positiveIntEnv("RESPONDING_POLL_INTERVAL", &c.PollIntervalSeconds); err != nil {
		return Config{}, err
	}
	if err := positiveIntEnv("RESPONDING_SPOOL_MAX_BATCHES", &c.SpoolMaxBatches); err != nil {
		return Config{}, err
	}
	if err := positiveIntEnv("RESPONDING_MAX_CONCURRENT_CHECKS", &c.MaxConcurrentChecks); err != nil {
		return Config{}, err
	}
	return c, c.Validate()
}

// positiveIntEnv overwrites *dst with the integer in the named environment
// variable when it is set, requiring a positive value.
func positiveIntEnv(name string, dst *int) error {
	v := os.Getenv(name)
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fmt.Errorf("%s must be a positive integer", name)
	}
	*dst = n
	return nil
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
