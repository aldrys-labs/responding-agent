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
	"golang.org/x/net/ipv6"
)

// IANA protocol numbers for ICMP.
const (
	protocolICMP   = 1  // IPv4
	protocolICMPv6 = 58 // IPv6
)

// pingSeq hands out a distinct sequence number to every echo request so
// concurrent ping checks (which may share the unprivileged ICMP socket space)
// never accept each other's replies.
var pingSeq atomic.Uint32

// pingTransport is the ICMP flavor for one address family: which datagram
// network to listen on, which protocol number to parse replies with, and the
// echo request/reply message types.
type pingTransport struct {
	network   string
	listen    string
	proto     int
	echoType  icmp.Type
	replyType icmp.Type
}

// transportFor selects the ICMP transport matching the resolved address
// family, so IPv6-only targets are pinged over ICMPv6 instead of never
// resolving.
func transportFor(ip net.IP) pingTransport {
	if ip.To4() != nil {
		return pingTransport{
			network:   "udp4",
			listen:    "0.0.0.0",
			proto:     protocolICMP,
			echoType:  ipv4.ICMPTypeEcho,
			replyType: ipv4.ICMPTypeEchoReply,
		}
	}
	return pingTransport{
		network:   "udp6",
		listen:    "::",
		proto:     protocolICMPv6,
		echoType:  ipv6.ICMPTypeEchoRequest,
		replyType: ipv6.ICMPTypeEchoReply,
	}
}

// runPing sends a single ICMP echo request and waits for the matching reply.
// The target is resolved over both address families and the echo goes out over
// the matching flavor (ICMP on udp4, ICMPv6 on udp6). It uses an unprivileged
// datagram ICMP socket, which works without root on macOS and on Linux when
// net.ipv4.ping_group_range permits it; otherwise the socket fails to open and
// the check is reported as down.
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

	ipAddr, err := net.ResolveIPAddr("ip", c.Target)
	if err != nil {
		return outcome{status: protocol.StatusDown, err: fmt.Errorf("resolve: %w", err)}
	}
	tr := transportFor(ipAddr.IP)

	conn, err := icmp.ListenPacket(tr.network, tr.listen)
	if err != nil {
		return outcome{status: protocol.StatusDown, err: fmt.Errorf("icmp socket (needs privilege or ping_group_range): %w", err)}
	}
	defer conn.Close()

	id := os.Getpid() & 0xffff
	seq := int(pingSeq.Add(1) & 0xffff)
	marker := []byte(fmt.Sprintf("respondi.ng:%d", seq))
	msg := icmp.Message{
		Type: tr.echoType,
		Code: 0,
		Body: &icmp.Echo{ID: id, Seq: seq, Data: marker},
	}
	wire, err := msg.Marshal(nil)
	if err != nil {
		return outcome{status: protocol.StatusDown, err: err}
	}

	start := time.Now()
	// For datagram ICMP the kernel assigns the source port, so the zero port is fine.
	if _, err := conn.WriteTo(wire, &net.UDPAddr{IP: ipAddr.IP, Zone: ipAddr.Zone}); err != nil {
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
		parsed, err := icmp.ParseMessage(tr.proto, reply[:n])
		if err != nil {
			continue
		}
		if parsed.Type != tr.replyType {
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
// *net.UDPAddr on a datagram ICMP socket and a *net.IPAddr on a raw one.
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
