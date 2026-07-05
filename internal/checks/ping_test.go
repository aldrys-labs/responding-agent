package checks

import (
	"context"
	"net"
	"testing"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
	"golang.org/x/net/icmp"
)

// pingAvailable reports whether this environment lets us open an unprivileged
// ICMP socket for the given family. Many CI runners do not, so ping tests skip
// rather than fail.
func pingAvailable(t *testing.T, network, listen string) {
	t.Helper()
	conn, err := icmp.ListenPacket(network, listen)
	if err != nil {
		t.Skipf("unprivileged ICMP not available here (%s): %v", network, err)
	}
	conn.Close()
}

func TestPingLoopbackUp(t *testing.T) {
	pingAvailable(t, "udp4", "0.0.0.0")
	r := NewRunner()
	got := r.Run(context.Background(), protocol.Check{
		ID: "p", Type: protocol.CheckPing, Target: "127.0.0.1", TimeoutMs: 2000,
	})
	if got.Status != protocol.StatusUp {
		t.Errorf("loopback ping: status = %q, want up (err=%q)", got.Status, got.Error)
	}
}

func TestPingLoopbackV6Up(t *testing.T) {
	pingAvailable(t, "udp6", "::")
	if _, err := net.ResolveIPAddr("ip6", "::1"); err != nil {
		t.Skipf("IPv6 loopback not available: %v", err)
	}
	r := NewRunner()
	got := r.Run(context.Background(), protocol.Check{
		ID: "p6", Type: protocol.CheckPing, Target: "::1", TimeoutMs: 2000,
	})
	if got.Status != protocol.StatusUp {
		t.Errorf("v6 loopback ping: status = %q, want up (err=%q)", got.Status, got.Error)
	}
}

// transportFor is the family dispatch behind the socketless part of the ping
// check; it must pick ICMPv6 for v6 addresses so IPv6-only targets resolve and
// get pinged at all (issue #12). This runs everywhere, raw sockets or not.
func TestPingTransportSelection(t *testing.T) {
	v4 := transportFor(net.ParseIP("192.0.2.1"))
	if v4.network != "udp4" || v4.proto != protocolICMP {
		t.Errorf("v4 transport = %+v, want udp4/ICMP", v4)
	}
	v6 := transportFor(net.ParseIP("2001:db8::1"))
	if v6.network != "udp6" || v6.proto != protocolICMPv6 {
		t.Errorf("v6 transport = %+v, want udp6/ICMPv6", v6)
	}
	mapped := transportFor(net.ParseIP("::ffff:192.0.2.1"))
	if mapped.network != "udp4" {
		t.Errorf("v4-mapped transport = %+v, want udp4", mapped)
	}
}

func TestPingUnreachableDown(t *testing.T) {
	pingAvailable(t, "udp4", "0.0.0.0")
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
