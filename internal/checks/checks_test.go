package checks

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aldrys-labs/responding-agent/internal/protocol"
)

func TestHTTPCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
		case "/teapot":
			w.WriteHeader(http.StatusTeapot)
		case "/slow":
			time.Sleep(40 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	r := NewRunner()
	ctx := context.Background()

	tests := []struct {
		name string
		chk  protocol.Check
		want protocol.Status
	}{
		{
			name: "up on expected status",
			chk:  protocol.Check{ID: "a", Type: protocol.CheckHTTP, Target: srv.URL + "/ok", ExpectedStatus: 200, TimeoutMs: 2000},
			want: protocol.StatusUp,
		},
		{
			name: "up on any 2xx when no expected status",
			chk:  protocol.Check{ID: "b", Type: protocol.CheckHTTP, Target: srv.URL + "/ok", TimeoutMs: 2000},
			want: protocol.StatusUp,
		},
		{
			name: "down on unexpected status",
			chk:  protocol.Check{ID: "c", Type: protocol.CheckHTTP, Target: srv.URL + "/teapot", ExpectedStatus: 200, TimeoutMs: 2000},
			want: protocol.StatusDown,
		},
		{
			name: "down on connection error",
			chk:  protocol.Check{ID: "d", Type: protocol.CheckHTTP, Target: "http://127.0.0.1:1", ExpectedStatus: 200, TimeoutMs: 500},
			want: protocol.StatusDown,
		},
		{
			name: "degraded when slower than threshold",
			chk:  protocol.Check{ID: "e", Type: protocol.CheckHTTP, Target: srv.URL + "/slow", ExpectedStatus: 200, TimeoutMs: 2000, DegradedAboveMs: 10},
			want: protocol.StatusDegraded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Run(ctx, tt.chk)
			if got.Status != tt.want {
				t.Fatalf("status = %q, want %q (err=%q)", got.Status, tt.want, got.Error)
			}
			if got.CheckID != tt.chk.ID {
				t.Errorf("checkID = %q, want %q", got.CheckID, tt.chk.ID)
			}
			if got.Timestamp == "" {
				t.Error("timestamp is empty")
			}
		})
	}
}

func TestTCPCheck(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	r := NewRunner()
	ctx := context.Background()

	up := r.Run(ctx, protocol.Check{ID: "tcp-up", Type: protocol.CheckTCP, Target: ln.Addr().String(), TimeoutMs: 1000})
	if up.Status != protocol.StatusUp {
		t.Errorf("open port: status = %q, want up (err=%q)", up.Status, up.Error)
	}

	down := r.Run(ctx, protocol.Check{ID: "tcp-down", Type: protocol.CheckTCP, Target: "127.0.0.1:1", TimeoutMs: 500})
	if down.Status != protocol.StatusDown {
		t.Errorf("closed port: status = %q, want down", down.Status)
	}
}

func TestTLSDownOnNonTLSListener(t *testing.T) {
	// A plain TCP listener that never speaks TLS: the handshake must fail and the
	// check must be reported down.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
		}
	}()

	r := NewRunner()
	got := r.Run(context.Background(), protocol.Check{ID: "tls", Type: protocol.CheckTLS, Target: ln.Addr().String(), TimeoutMs: 1000})
	if got.Status != protocol.StatusDown {
		t.Errorf("status = %q, want down", got.Status)
	}
}

func TestEvaluateLeaf(t *testing.T) {
	now := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	latency := 5 * time.Millisecond

	tests := []struct {
		name        string
		notAfter    time.Time
		warningDays int
		want        protocol.Status
	}{
		{"valid far future, no warning window", now.Add(365 * 24 * time.Hour), 0, protocol.StatusUp},
		{"valid beyond warning window", now.Add(30 * 24 * time.Hour), 14, protocol.StatusUp},
		{"within warning window", now.Add(5 * 24 * time.Hour), 14, protocol.StatusDegraded},
		{"exactly outside window", now.Add(15 * 24 * time.Hour), 14, protocol.StatusUp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			leaf := &x509.Certificate{NotAfter: tt.notAfter}
			chk := protocol.Check{Type: protocol.CheckTLS, TLSExpiryWarningDays: tt.warningDays}
			got := evaluateLeaf(chk, leaf, latency, now)
			if got.status != tt.want {
				t.Errorf("status = %q, want %q", got.status, tt.want)
			}
		})
	}
}

func TestUnknownCheckType(t *testing.T) {
	r := NewRunner()
	got := r.Run(context.Background(), protocol.Check{ID: "x", Type: "bogus", Target: "whatever"})
	if got.Status != protocol.StatusDown {
		t.Errorf("status = %q, want down", got.Status)
	}
	if got.Error == "" {
		t.Error("expected an error message for unknown type")
	}
}
