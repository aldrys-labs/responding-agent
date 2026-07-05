package checks

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// A check that expects a 3xx must evaluate the redirect response itself, while
// other checks keep following redirects (issue #7).
func TestHTTPRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	r := NewRunner()
	tests := []struct {
		name string
		chk  protocol.Check
		want protocol.Status
	}{
		{
			name: "expected 302 sees the redirect, not its destination",
			chk:  protocol.Check{ID: "a", Type: protocol.CheckHTTP, Target: srv.URL + "/redirect", ExpectedStatus: 302, TimeoutMs: 2000},
			want: protocol.StatusUp,
		},
		{
			name: "expected 200 still follows the redirect",
			chk:  protocol.Check{ID: "b", Type: protocol.CheckHTTP, Target: srv.URL + "/redirect", ExpectedStatus: 200, TimeoutMs: 2000},
			want: protocol.StatusUp,
		},
		{
			name: "no expected status still follows the redirect",
			chk:  protocol.Check{ID: "c", Type: protocol.CheckHTTP, Target: srv.URL + "/redirect", TimeoutMs: 2000},
			want: protocol.StatusUp,
		},
		{
			name: "expected 301 does not match a 302",
			chk:  protocol.Check{ID: "d", Type: protocol.CheckHTTP, Target: srv.URL + "/redirect", ExpectedStatus: 301, TimeoutMs: 2000},
			want: protocol.StatusDown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Run(context.Background(), tt.chk)
			if got.Status != tt.want {
				t.Errorf("status = %q, want %q (err=%q)", got.Status, tt.want, got.Error)
			}
		})
	}
}

// A Host header must set the request's vhost, which Go's client reads from
// req.Host rather than the header map.
func TestHTTPHostHeader(t *testing.T) {
	var mu sync.Mutex
	seenHost := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenHost = r.Host
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewRunner()
	got := r.Run(context.Background(), protocol.Check{
		ID: "h", Type: protocol.CheckHTTP, Target: srv.URL, TimeoutMs: 2000,
		Headers: map[string]string{"Host": "vhost.example"},
	})
	if got.Status != protocol.StatusUp {
		t.Fatalf("status = %q, want up (err=%q)", got.Status, got.Error)
	}
	mu.Lock()
	defer mu.Unlock()
	if seenHost != "vhost.example" {
		t.Errorf("server saw host %q, want %q", seenHost, "vhost.example")
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

// An expired certificate must be down even with InsecureSkipVerify, where the
// handshake itself validates nothing (issue #6).
func TestTLSExpiredCertWithInsecureSkipVerify(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-24 * time.Hour), // already expired
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	})
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
			go func(c net.Conn) {
				_ = c.(*tls.Conn).Handshake()
				c.Close()
			}(c)
		}
	}()

	r := NewRunner()
	got := r.Run(context.Background(), protocol.Check{
		ID: "tls", Type: protocol.CheckTLS, Target: ln.Addr().String(),
		TimeoutMs: 2000, InsecureSkipVerify: true,
	})
	if got.Status != protocol.StatusDown {
		t.Errorf("status = %q, want down (err=%q)", got.Status, got.Error)
	}
	if !strings.Contains(got.Error, "expired") {
		t.Errorf("error = %q, want it to mention the expiry", got.Error)
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
		{"expired, no warning window", now.Add(-3 * 24 * time.Hour), 0, protocol.StatusDown},
		{"expired, with warning window", now.Add(-3 * 24 * time.Hour), 14, protocol.StatusDown},
		{"expired this instant", now, 0, protocol.StatusDown},
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

// Two runs of the same check completing within one wall second must carry
// distinct timestamps, or the backend's (monitor, agent, ts) dedupe silently
// drops one of them (issue #10).
func TestResultTimestampsAreSubSecond(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewRunner()
	chk := protocol.Check{ID: "ts", Type: protocol.CheckHTTP, Target: srv.URL, TimeoutMs: 2000}
	first := r.Run(context.Background(), chk)
	second := r.Run(context.Background(), chk)

	for _, res := range []protocol.Result{first, second} {
		ts, err := time.Parse(time.RFC3339Nano, res.Timestamp)
		if err != nil {
			t.Fatalf("timestamp %q does not parse as RFC3339Nano: %v", res.Timestamp, err)
		}
		if ts.Nanosecond() == 0 {
			t.Errorf("timestamp %q has no sub-second component", res.Timestamp)
		}
	}
	if first.Timestamp == second.Timestamp {
		t.Errorf("both runs share timestamp %q", first.Timestamp)
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
