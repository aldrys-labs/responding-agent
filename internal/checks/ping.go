package checks

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// protocolICMP is the IANA protocol number for ICMP (IPv4).
const protocolICMP = 1

// runPing sends a single ICMP echo request and waits for the reply. It uses an
// unprivileged datagram ICMP socket (udp4), which works without root on macOS
// and on Linux when net.ipv4.ping_group_range permits it; otherwise the socket
// fails to open and the check is reported as down with that error.
//
// Target is a host or IP (no port). Latency is the round-trip time.
func runPing(ctx context.Context, c protocol.Check) outcome {
	deadline := time.Now().Add(timeout(c))
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	ipAddr, err := net.ResolveIPAddr("ip4", c.Target)
	if err != nil {
		return outcome{status: protocol.StatusDown, err: fmt.Errorf("resolve: %w", err)}
	}

	conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		return outcome{status: protocol.StatusDown, err: fmt.Errorf("icmp socket (needs privilege or ping_group_range): %w", err)}
	}
	defer conn.Close()

	id := os.Getpid() & 0xffff
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{ID: id, Seq: 1, Data: []byte("respondi.ng")},
	}
	wire, err := msg.Marshal(nil)
	if err != nil {
		return outcome{status: protocol.StatusDown, err: err}
	}

	start := time.Now()
	// For udp4 ICMP the kernel rewrites the port, so the zero port is fine.
	if _, err := conn.WriteTo(wire, &net.UDPAddr{IP: ipAddr.IP}); err != nil {
		return outcome{status: protocol.StatusDown, err: fmt.Errorf("send: %w", err)}
	}

	if err := conn.SetReadDeadline(deadline); err != nil {
		return outcome{status: protocol.StatusDown, err: err}
	}

	reply := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(reply)
		latency := time.Since(start)
		if err != nil {
			return outcome{status: protocol.StatusDown, latency: latency, err: fmt.Errorf("no reply: %w", err)}
		}
		parsed, err := icmp.ParseMessage(protocolICMP, reply[:n])
		if err != nil {
			continue
		}
		if parsed.Type == ipv4.ICMPTypeEchoReply {
			if echo, ok := parsed.Body.(*icmp.Echo); ok && echo.ID == id {
				return outcome{status: classifyLatency(c, latency), latency: latency}
			}
			// Reply for a different id (another pinger sharing the socket
			// space): keep reading until our own reply or the deadline.
			continue
		}
	}
}
