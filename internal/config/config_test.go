package config

import "testing"

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, c Config)
	}{
		{
			name:    "neither backend nor file",
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name:    "backend without token",
			env:     map[string]string{"RESPONDING_BACKEND_URL": "https://x.example"},
			wantErr: true,
		},
		{
			name: "backend with token",
			env: map[string]string{
				"RESPONDING_BACKEND_URL": "https://x.example/",
				"RESPONDING_AGENT_TOKEN": "secret",
			},
			check: func(t *testing.T, c Config) {
				if c.BackendURL != "https://x.example" {
					t.Errorf("trailing slash not trimmed: %q", c.BackendURL)
				}
				if !c.DispatchEnabled() {
					t.Error("dispatch should be enabled")
				}
			},
		},
		{
			name: "local file only is valid",
			env:  map[string]string{"RESPONDING_CHECKS_FILE": "/tmp/checks.json"},
			check: func(t *testing.T, c Config) {
				if c.DispatchEnabled() {
					t.Error("dispatch should be disabled without backend+token")
				}
			},
		},
		{
			name: "custom poll interval",
			env: map[string]string{
				"RESPONDING_CHECKS_FILE":   "/tmp/checks.json",
				"RESPONDING_POLL_INTERVAL": "15",
			},
			check: func(t *testing.T, c Config) {
				if c.PollIntervalSeconds != 15 {
					t.Errorf("poll = %d, want 15", c.PollIntervalSeconds)
				}
			},
		},
		{
			name: "invalid poll interval",
			env: map[string]string{
				"RESPONDING_CHECKS_FILE":   "/tmp/checks.json",
				"RESPONDING_POLL_INTERVAL": "nope",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"RESPONDING_BACKEND_URL", "RESPONDING_AGENT_TOKEN", "RESPONDING_CHECKS_FILE", "RESPONDING_POLL_INTERVAL"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			c, err := Load("")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, c)
			}
		})
	}
}

func TestChecksFileOverride(t *testing.T) {
	t.Setenv("RESPONDING_CHECKS_FILE", "/from/env.json")
	c, err := Load("/from/flag.json")
	if err != nil {
		t.Fatal(err)
	}
	if c.ChecksFile != "/from/flag.json" {
		t.Errorf("ChecksFile = %q, want flag override", c.ChecksFile)
	}
}
