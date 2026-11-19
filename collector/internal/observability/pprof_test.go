package observability

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoopbackAddr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		addr string
		want string
	}{
		{"port only", ":6060", "127.0.0.1:6060"},
		{"ipv4 wildcard", "0.0.0.0:6060", "127.0.0.1:6060"},
		{"ipv6 wildcard", "[::]:6060", "[::1]:6060"},
		{"ipv4 loopback", "127.0.0.1:6060", "127.0.0.1:6060"},
		{"ipv6 loopback", "[::1]:6060", "[::1]:6060"},
		{"localhost", "localhost:6060", "127.0.0.1:6060"},
		{"explicit remote ipv4", "192.0.2.10:6060", "127.0.0.1:6060"},
		{"explicit remote ipv6", "[2001:db8::10]:6060", "127.0.0.1:6060"},
		{"ipv6 wildcard ephemeral", "[::]:0", "[::1]:0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := loopbackAddr(tc.addr); got != tc.want {
				t.Fatalf("loopbackAddr(%q) = %q, want %q", tc.addr, got, tc.want)
			}
		})
	}
}

func TestLoopbackAddrBindsToLoopback(t *testing.T) {
	tests := []struct {
		addr   string
		wantIP string
	}{
		{":0", "127.0.0.1"},
		{"0.0.0.0:0", "127.0.0.1"},
		{"[::]:0", "::1"},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			ln, err := net.Listen("tcp", loopbackAddr(tc.addr))
			if err != nil {
				if tc.wantIP == "::1" {
					t.Skipf("IPv6 loopback unavailable in this environment: %v", err)
				}
				t.Fatalf("net.Listen(%q): %v", loopbackAddr(tc.addr), err)
			}
			defer ln.Close()

			tcpAddr, ok := ln.Addr().(*net.TCPAddr)
			if !ok {
				t.Fatalf("unexpected listener address type %T", ln.Addr())
			}
			if !tcpAddr.IP.IsLoopback() || tcpAddr.IP.String() != tc.wantIP {
				t.Fatalf("wildcard %q bound to %s, want loopback %s", tc.addr, tcpAddr.IP, tc.wantIP)
			}
		})
	}
}

func TestLocalhostOnly(t *testing.T) {
	t.Parallel()
	called := false
	guard := localhostOnly(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name   string
		remote string
		want   int
	}{
		{"ipv4 loopback", "127.0.0.1:1234", http.StatusOK},
		{"ipv6 loopback", "[::1]:1234", http.StatusOK},
		{"remote ipv4", "198.51.100.7:1234", http.StatusForbidden},
		{"remote ipv6", "[2001:db8::7]:1234", http.StatusForbidden},
		{"malformed", "not-an-address", http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			r := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
			r.RemoteAddr = tc.remote
			w := httptest.NewRecorder()
			guard(w, r)

			if w.Code != tc.want {
				t.Fatalf("RemoteAddr %q: status = %d, want %d", tc.remote, w.Code, tc.want)
			}
			if wantCalled := tc.want == http.StatusOK; called != wantCalled {
				t.Fatalf("RemoteAddr %q: handler called = %t, want %t", tc.remote, called, wantCalled)
			}
		})
	}
}

func TestPprofMuxGuardsEveryHandler(t *testing.T) {
	t.Parallel()
	mux := newPprofMux()
	endpoints := []string{
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/profile",
		"/debug/pprof/symbol",
		"/debug/pprof/trace",
	}
	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			// The guard runs before pprof handlers, so no profile is collected
			// for a rejected request.
			r := httptest.NewRequest(http.MethodGet, endpoint+"?seconds=0", nil)
			r.RemoteAddr = "198.51.100.7:1234"
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("remote GET %s = %d, want 403", endpoint, w.Code)
			}
		})
	}

	r := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("loopback GET /debug/pprof/ = %d, want 200", w.Code)
	"bytes"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func capturePprofLogs(t *testing.T) *syncBuffer {
	t.Helper()
	logs := &syncBuffer{}
	previous := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	return logs
}

func waitForPprofLog(t *testing.T, logs *syncBuffer, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if out := logs.String(); strings.Contains(out, want) {
			return out
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in logs; got %q", want, logs.String())
	return ""
}

func TestRegisterAndServePprofLogsBindFailure(t *testing.T) {
	logs := capturePprofLogs(t)

	// Reserve a loopback port so the pprof listener cannot bind to it.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer occupied.Close()

	RegisterAndServePprof(occupied.Addr().String())

	out := waitForPprofLog(t, logs, "pprof server failed")
	if strings.Contains(out, "pprof listening") {
		t.Fatalf("success logged despite bind failure: %q", out)
	}
}

func TestRegisterAndServePprofServesAfterBind(t *testing.T) {
	logs := capturePprofLogs(t)

	RegisterAndServePprof("127.0.0.1:0")

	out := waitForPprofLog(t, logs, "pprof listening on ")
	match := regexp.MustCompile(`pprof listening on (\S+)`).FindStringSubmatch(out)
	if match == nil {
		t.Fatalf("no bound address in log: %q", out)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + match[1] + "/debug/pprof/")
	if err != nil {
		t.Fatalf("GET pprof index: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET pprof index status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
