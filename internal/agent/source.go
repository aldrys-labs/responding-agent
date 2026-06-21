package agent

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

// loadChecksFile reads a ConfigResponse from a local JSON file. This backs the
// simplest self-host and offline-test modes, where the agent does not pull its
// configuration from a backend.
func loadChecksFile(path string) (protocol.ConfigResponse, error) {
	var cfg protocol.ConfigResponse
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read checks file: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse checks file %s: %w", path, err)
	}
	return cfg, nil
}
