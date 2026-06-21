package checks

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// protocolICMP is the IANA protocol number for ICMP (IPv4).
const protocolICMP = 1

// pingSeq hands out a distinct sequence number to every echo request so
// concurrent ping checks (which may share the unprivileged ICMP socket space)
// never accept each other's replies.
var pingSeq atomic.Uint32

// runPing sends a single ICMP echo request and waits for the matching reply. It
// uses an unprivileged datagram ICMP socket (udp4), which works without root on
// macOS and on Linux when net.ipv4.ping_group_range permits it; otherwise the
// socket fails to open and the check is reported as down.
//
// A reply is only accepted when it comes from the target IP and carries our
// echo id, sequence and payload marker. Without those guards an unreachable
// target could be reported up by picking up a stray reply meant for another
// check sharing the socket space.
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
	seq := int(pingSeq.Add(1) & 0xffff)
	marker := []byte(fmt.Sprintf("respondi.ng:%d", seq))
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{ID: id, Seq: seq, Data: marker},
	}
	wire, err := msg.Marshal(nil)
	if err != nil {
		return outcome{status: protocol.StatusDown, err: err}
	}

	start := time.Now()
	// For udp4 ICMP the kernel assigns the source port, so the zero port is fine.
	if _, err := conn.WriteTo(wire, &net.UDPAddr{IP: ipAddr.IP}); err != nil {
		return outcome{status: protocol.StatusDown, err: fmt.Errorf("send: %w", err)}
	}

	if err := conn.SetReadDeadline(deadline); err != nil {
		return outcome{status: protocol.StatusDown, err: err}
	}

	reply := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(reply)
		latency := time.Since(start)
		if err != nil {
			return outcome{status: protocol.StatusDown, latency: latency, err: fmt.Errorf("no reply: %w", err)}
		}
		// The reply must come from the target IP; anything else is a stray
		// packet (or another check's reply) and is ignored.
		if !peerIP(peer).Equal(ipAddr.IP) {
			continue
		}
		parsed, err := icmp.ParseMessage(protocolICMP, reply[:n])
		if err != nil {
			continue
		}
		if parsed.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		echo, ok := parsed.Body.(*icmp.Echo)
		if !ok || echo.Seq != seq || !bytes.Equal(echo.Data, marker) {
			// Not our echo: keep reading until our reply or the deadline.
			continue
		}
		return outcome{status: classifyLatency(c, latency), latency: latency}
	}
}

// peerIP extracts the IP from the address returned by ReadFrom, which is a
// *net.UDPAddr on a udp4 ICMP socket and a *net.IPAddr on a raw one.
func peerIP(addr net.Addr) net.IP {
	switch a := addr.(type) {
	case *net.UDPAddr:
		return a.IP
	case *net.IPAddr:
		return a.IP
	default:
		return nil
	}
}
