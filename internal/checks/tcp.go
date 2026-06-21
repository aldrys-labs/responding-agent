package checks

import (
	"context"
	"net"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

// runTCP opens a TCP connection to the target (host:port) and reports up if the
// connection is established within the timeout, down otherwise. Latency is the
// time to establish the connection.
func runTCP(ctx context.Context, c protocol.Check) outcome {
	ctx, cancel := context.WithTimeout(ctx, timeout(c))
	defer cancel()

	var d net.Dialer
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", c.Target)
	latency := time.Since(start)
	if err != nil {
		return outcome{status: protocol.StatusDown, latency: latency, err: err}
	}
	conn.Close()
	return outcome{status: classifyLatency(c, latency), latency: latency}
}
