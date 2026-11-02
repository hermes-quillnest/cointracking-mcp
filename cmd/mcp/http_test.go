package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ct "cointracking-mcp/pkg/cointracking"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef"

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`

func post(t *testing.T, h http.Handler, auth string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPRequiresBearerToken(t *testing.T) {
	h := newHTTPHandler(newMCPServer(ct.NewClient("k", "s")), testToken)

	for name, auth := range map[string]string{
		"missing":      "",
		"wrong":        "Bearer nope",
		"wrong scheme": "Basic " + testToken,
		"bare token":   testToken,
		"prefix only":  "Bearer ",
		"token prefix": "Bearer " + testToken[:len(testToken)-1],
		"token suffix": "Bearer " + testToken + "x",
	} {
		rec := post(t, h, auth, initBody)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rec.Code)
		}
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: missing WWW-Authenticate header", name)
		}
	}
}

func TestHTTPInitializeAndListTools(t *testing.T) {
	h := newHTTPHandler(newMCPServer(ct.NewClient("k", "s")), testToken)
	auth := "Bearer " + testToken

	rec := post(t, h, auth, initBody)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"serverInfo"`) {
		t.Fatalf("initialize: status %d body %s", rec.Code, rec.Body.String())
	}

	rec = post(t, h, auth, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list: status %d body %s", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"cointracking_get_balance", "cointracking_get_grouped_balance", "cointracking_get_trades", "cointracking_call"} {
		if !strings.Contains(rec.Body.String(), name) {
			t.Errorf("tools/list missing %s: %s", name, rec.Body.String())
		}
	}
}

func TestHTTPHealthzIsOpenAndLeaksNothing(t *testing.T) {
	h := newHTTPHandler(newMCPServer(ct.NewClient("k", "s")), testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("healthz: %d %q", rec.Code, rec.Body.String())
	}
}

func TestServeHTTPRejectsWeakToken(t *testing.T) {
	for _, tok := range []string{"", "short"} {
		if err := serveHTTP(t.Context(), "127.0.0.1:0", tok, "", "", nil); err == nil {
			t.Errorf("token %q accepted, want error", tok)
		}
	}
	if err := serveHTTP(t.Context(), "127.0.0.1:0", testToken, "cert.pem", "", nil); err == nil {
		t.Error("cert without key accepted, want error")
	}
}
