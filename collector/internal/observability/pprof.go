package observability

import (
	"log"
	"net"
	"net/http"
	"net/http/pprof"
)

// localhostOnly wraps an http.HandlerFunc so it only responds to requests
// from a loopback address. Remote requests receive 403 Forbidden. This mirrors
// the guard used by the main collector HTTP server and provides defense in
// depth even if the pprof listener is misconfigured.
func localhostOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// loopbackAddr rewrites addr so the pprof server can only ever listen on a
// loopback interface. Wildcard hosts (":6060", "0.0.0.0:6060", "[::]:6060")
// are mapped to their loopback equivalents, and any explicit non-loopback host
// is replaced with 127.0.0.1. Already-loopback addresses and the port are
// preserved.
func loopbackAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// A malformed address cannot be served by http.ListenAndServe anyway;
		// leave it unchanged so the listener reports the error.
		return addr
	}

	if ip := net.ParseIP(host); ip != nil {
		switch {
		case ip.IsLoopback():
			return addr
		case ip.IsUnspecified() && ip.To4() == nil:
			host = "::1"
		default:
			host = "127.0.0.1"
		}
	} else {
		// Empty host or a name (including "localhost"): force loopback.
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// newPprofMux builds the pprof route table with every handler restricted to
// loopback clients.
func newPprofMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", localhostOnly(pprof.Index))
	mux.HandleFunc("/debug/pprof/cmdline", localhostOnly(pprof.Cmdline))
	mux.HandleFunc("/debug/pprof/profile", localhostOnly(pprof.Profile))
	mux.HandleFunc("/debug/pprof/symbol", localhostOnly(pprof.Symbol))
	mux.HandleFunc("/debug/pprof/trace", localhostOnly(pprof.Trace))
	return mux
}

// RegisterAndServePprof starts a dedicated pprof HTTP server on addr (e.g. ":6060").
// The listener is always forced to a loopback interface and every handler also
// rejects non-loopback clients, so profiles are never remotely reachable.
func RegisterAndServePprof(addr string) {
	addr = loopbackAddr(addr)
	mux := newPprofMux()
	go func() {
		log.Printf("pprof listening on %s", addr)
		_ = http.ListenAndServe(addr, mux)
	}()
}
