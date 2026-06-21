package metrics

import (
	"strings"
	"testing"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

func TestRecordResultAndReady(t *testing.T) {
	m := New(1000)
	if m.Ready() {
		t.Error("should not be ready before any config reload")
	}
	m.ConfigReloads.Add(1)
	if !m.Ready() {
		t.Error("should be ready after a config reload")
	}

	m.RecordResult(protocol.StatusUp)
	m.RecordResult(protocol.StatusUp)
	m.RecordResult(protocol.StatusDegraded)
	m.RecordResult(protocol.StatusDown)

	if got := m.ChecksRun.Load(); got != 4 {
		t.Errorf("checks run = %d, want 4", got)
	}
	if got := m.CheckUp.Load(); got != 2 {
		t.Errorf("up = %d, want 2", got)
	}
	if got := m.CheckDegraded.Load(); got != 1 {
		t.Errorf("degraded = %d, want 1", got)
	}
	if got := m.CheckDown.Load(); got != 1 {
		t.Errorf("down = %d, want 1", got)
	}
}

func TestRenderContainsSeries(t *testing.T) {
	m := New(1000)
	m.RecordResult(protocol.StatusUp)
	m.ResultsPosted.Add(3)
	m.SpoolDepth.Store(2)

	out := m.Render()
	for _, want := range []string{
		"responding_agent_start_time_seconds 1000",
		"responding_agent_checks_up_total 1",
		"responding_agent_results_posted_total 3",
		"responding_agent_spool_depth 2",
		"# TYPE responding_agent_checks_run_total counter",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics output missing %q\n---\n%s", want, out)
		}
	}
}
