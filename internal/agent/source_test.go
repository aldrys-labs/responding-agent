package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

func TestLoadChecksFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checks.json")
	content := `{
		"pollIntervalSeconds": 45,
		"checks": [
			{"id": "web", "type": "http", "target": "https://example.com", "intervalSeconds": 30, "timeoutMs": 5000, "expectedStatus": 200}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadChecksFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollIntervalSeconds != 45 {
		t.Errorf("poll = %d, want 45", cfg.PollIntervalSeconds)
	}
	if len(cfg.Checks) != 1 || cfg.Checks[0].Type != protocol.CheckHTTP {
		t.Fatalf("unexpected checks: %+v", cfg.Checks)
	}
}

func TestLoadChecksFileMissing(t *testing.T) {
	if _, err := loadChecksFile("/no/such/file.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}
