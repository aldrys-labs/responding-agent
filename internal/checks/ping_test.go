package checks

import (
	"context"
	"testing"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
	"golang.org/x/net/icmp"
)

// pingAvailable reports whether this environment lets us open an unprivileged
// ICMP socket. Many CI runners do not, so ping tests skip rather than fail.
func pingAvailable(t *testing.T) {
	t.Helper()
	conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		t.Skipf("unprivileged ICMP not available here: %v", err)
	}
	conn.Close()
}

func TestPingLoopbackUp(t *testing.T) {
	pingAvailable(t)
	r := NewRunner()
	got := r.Run(context.Background(), protocol.Check{
		ID: "p", Type: protocol.CheckPing, Target: "127.0.0.1", TimeoutMs: 2000,
	})
	if got.Status != protocol.StatusUp {
		t.Errorf("loopback ping: status = %q, want up (err=%q)", got.Status, got.Error)
	}
}

func TestPingUnreachableDown(t *testing.T) {
	pingAvailable(t)
	r := NewRunner()
	// 192.0.2.1 is TEST-NET-1 (RFC 5737): guaranteed not to answer. A correct
	// runner must not accept a stray reply from another check as this one's.
	got := r.Run(context.Background(), protocol.Check{
		ID: "p", Type: protocol.CheckPing, Target: "192.0.2.1", TimeoutMs: 800,
	})
	if got.Status != protocol.StatusDown {
		t.Errorf("unreachable ping: status = %q, want down", got.Status)
	}
}

func TestPingUnresolvable(t *testing.T) {
	r := NewRunner()
	got := r.Run(context.Background(), protocol.Check{
		ID: "p", Type: protocol.CheckPing, Target: "no-such-host.invalid", TimeoutMs: 800,
	})
	if got.Status != protocol.StatusDown {
		t.Errorf("unresolvable ping: status = %q, want down", got.Status)
	}
}
