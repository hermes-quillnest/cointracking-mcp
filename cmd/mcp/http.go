package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// minTokenLen is the shortest MCP_AUTH_TOKEN we accept. `openssl rand -hex 32`
// gives 64 characters.
const minTokenLen = 32

// newHTTPHandler serves the MCP server as a stateless Streamable HTTP
// endpoint at /mcp, protected by a static bearer token. GET /healthz is
// unauthenticated and returns no account data.
//
// Every tool is a plain request/response call, so stateless mode is enough:
// no sessions to track, and a restart never invalidates a client.
func newHTTPHandler(srv *mcp.Server, token string) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	// Browsers send Origin/Sec-Fetch-Site and are rejected cross-origin;
	// MCP clients (no browser headers) pass through.
	protected := http.NewCrossOriginProtection().Handler(mcpHandler)
	mux.Handle("/mcp", requireBearer(token, protected))
	return mux
}

// requireBearer rejects requests that don't carry "Authorization: Bearer
// <token>". Tokens are compared as SHA-256 digests in constant time, so
// neither content nor length leaks through timing. The header value is never
// logged.
func requireBearer(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		sum := sha256.Sum256([]byte(got))
		if !ok || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			log.Printf("rejected unauthenticated request from %s", r.RemoteAddr)
			w.Header().Set("WWW-Authenticate", `Bearer realm="cointracking-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serveHTTP runs the HTTP server until ctx is cancelled, then shuts down
// gracefully. With certFile and keyFile set it serves TLS itself; otherwise
// it serves plain HTTP, which is only appropriate on localhost or behind a
// TLS-terminating proxy / VPN (the bearer token travels in a header).
func serveHTTP(ctx context.Context, addr, token, certFile, keyFile string, srv *mcp.Server) error {
	if len(token) < minTokenLen {
		return fmt.Errorf("MCP_AUTH_TOKEN must be set and at least %d characters (generate one with `openssl rand -hex 32`)", minTokenLen)
	}
	if (certFile == "") != (keyFile == "") {
		return errors.New("-tls-cert and -tls-key must be given together")
	}

	hs := &http.Server{
		Addr:              addr,
		Handler:           newHTTPHandler(srv, token),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute, // CoinTracking calls can be slow
		IdleTimeout:       2 * time.Minute,
	}

	errc := make(chan error, 1)
	go func() {
		if certFile != "" {
			log.Printf("listening on https://%s/mcp", addr)
			errc <- hs.ListenAndServeTLS(certFile, keyFile)
			return
		}
		log.Printf("listening on http://%s/mcp (plain HTTP: keep it on localhost or behind TLS/VPN)", addr)
		errc <- hs.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return hs.Shutdown(sctx)
	}
}
