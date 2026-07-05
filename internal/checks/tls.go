package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

// runTLS performs a TLS handshake and inspects the leaf certificate expiry. The
// target is host:port; a bare host defaults to port 443. A check is:
//   - down on handshake failure, or when the leaf certificate is expired or
//     not yet valid (checked explicitly, since InsecureSkipVerify skips all
//     verification during the handshake);
//   - degraded when the certificate is valid but expires within
//     TLSExpiryWarningDays;
//   - up otherwise.
//
// Latency is the handshake time.
func runTLS(ctx context.Context, c protocol.Check) outcome {
	target := c.Target
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, "443")
	}
	host, _, _ := net.SplitHostPort(target)

	ctx, cancel := context.WithTimeout(ctx, timeout(c))
	defer cancel()

	dialer := &tls.Dialer{Config: &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: c.InsecureSkipVerify, //nolint:gosec // opt-in per check
	}}
	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", target)
	latency := time.Since(start)
	if err != nil {
		return outcome{status: protocol.StatusDown, latency: latency, err: err}
	}
	defer conn.Close()

	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return outcome{status: protocol.StatusDown, latency: latency, err: fmt.Errorf("no peer certificate")}
	}
	return evaluateLeaf(c, state.PeerCertificates[0], latency, time.Now())
}

// evaluateLeaf classifies the leaf certificate against its validity period and
// the check's expiry-warning window. Expiry is enforced here regardless of the
// warning setting: with InsecureSkipVerify (the usual mode for self-signed
// internal certs) the handshake validates nothing, so this is the only place an
// expired certificate can be caught. It is split out from the dial so the
// logic can be unit-tested without a network handshake. now is injected for the
// same reason.
func evaluateLeaf(c protocol.Check, leaf *x509.Certificate, latency time.Duration, now time.Time) outcome {
	remaining := leaf.NotAfter.Sub(now)
	if remaining <= 0 {
		return outcome{
			status:  protocol.StatusDown,
			latency: latency,
			err:     fmt.Errorf("certificate expired %d days ago (%s)", int(-remaining.Hours()/24), leaf.NotAfter.UTC().Format(time.RFC3339)),
		}
	}
	if now.Before(leaf.NotBefore) {
		return outcome{
			status:  protocol.StatusDown,
			latency: latency,
			err:     fmt.Errorf("certificate not valid before %s", leaf.NotBefore.UTC().Format(time.RFC3339)),
		}
	}
	if c.TLSExpiryWarningDays > 0 {
		warn := time.Duration(c.TLSExpiryWarningDays) * 24 * time.Hour
		if remaining < warn {
			return outcome{
				status:  protocol.StatusDegraded,
				latency: latency,
				err:     fmt.Errorf("certificate expires in %d days (%s)", int(remaining.Hours()/24), leaf.NotAfter.UTC().Format(time.RFC3339)),
			}
		}
	}
	return outcome{status: classifyLatency(c, latency), latency: latency}
}
