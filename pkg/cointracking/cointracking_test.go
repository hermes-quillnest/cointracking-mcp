package cointracking

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// startMock spins up a test server that mimics CoinTracking's flat response
// shape (data fields as siblings of success/method, no "return" wrapper) and
// dispatches on the "method" form field, like the real API does.
func startMock(t *testing.T, responses map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Key") == "" {
			t.Errorf("expected Key header to be set")
		}
		if r.Header.Get("Sign") == "" {
			t.Errorf("expected Sign header to be set")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		method := r.FormValue("method")
		if r.FormValue("nonce") == "" {
			t.Errorf("expected nonce to be set")
		}
		body, ok := responses[method]
		if !ok {
			t.Fatalf("mock has no canned response for method %q", method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// mockClient builds a Client pointed at the given test server.
func mockClient(srv *httptest.Server) *Client {
	c := NewClient("key", "secret")
	c.BaseURL = srv.URL
	return c
}

func TestGetBalance(t *testing.T) {
	// Flat shape matching the CoinTracking docs example: data fields are
	// siblings of success/method, NOT nested under a "return" key.
	srv := startMock(t, map[string]string{
		"getBalance": `{
			"success": 1,
			"method": "getBalance",
			"account_currency": "EUR",
			"details": {
				"BTC": {
					"amount": "1.18522859",
					"coin": "BTC",
					"value_fiat": "3625.69722281",
					"value_btc": "1.00000000",
					"price_fiat": "3059.07000000",
					"price_btc": "1.00000000",
					"change1h": "0.29",
					"change24h": "-0.61",
					"change7d": "-0.34",
					"change30d": "-14.60"
				}
			},
			"summary": {
				"value_fiat": "120114.45",
				"value_btc": "39.65021254",
				"profit_fiat": "107132.08",
				"profit_btc": "35.21109847"
			}
		}`,
	})

	got, err := mockClient(srv).GetBalance(context.Background())
	if err != nil {
		t.Fatalf("GetBalance() error = %v", err)
	}
	if got.AccountCurrency != "EUR" {
		t.Errorf("AccountCurrency = %q, want EUR", got.AccountCurrency)
	}
	btc, ok := got.Details["BTC"]
	if !ok {
		t.Fatalf("expected BTC entry in Details, got %+v", got.Details)
	}
	if btc.Amount != "1.18522859" {
		t.Errorf("BTC.Amount = %q, want 1.18522859", btc.Amount)
	}
	if got.Summary.ValueFiat != "120114.45" {
		t.Errorf("Summary.ValueFiat = %q, want 120114.45", got.Summary.ValueFiat)
	}
}

func TestGetGroupedBalance(t *testing.T) {
	srv := startMock(t, map[string]string{
		"getGroupedBalance": `{
			"success": 1,
			"method": "getGroupedBalance",
			"account_currency": "USD",
			"details": {
				"Kraken Balance": {
					"BTC": {"amount": "24.63757943", "fiat": "82725.01", "btc": "24.63757943"},
					"TOTAL_SUMMARY": {"fiat": "141288.18", "btc": "42.51419671"}
				}
			}
		}`,
	})

	got, err := mockClient(srv).GetGroupedBalance(context.Background(), GroupedBalanceOptions{ExcludeDepWith: true})
	if err != nil {
		t.Fatalf("GetGroupedBalance() error = %v", err)
	}
	kraken, ok := got.Details["Kraken Balance"]
	if !ok {
		t.Fatalf("expected 'Kraken Balance' group, got %+v", got.Details)
	}
	if kraken["BTC"].Fiat != "82725.01" {
		t.Errorf("Kraken BTC.Fiat = %q, want 82725.01", kraken["BTC"].Fiat)
	}
	if kraken["TOTAL_SUMMARY"].BTC != "42.51419671" {
		t.Errorf("Kraken TOTAL_SUMMARY.BTC = %q, want 42.51419671", kraken["TOTAL_SUMMARY"].BTC)
	}
}

func TestGetTrades(t *testing.T) {
	// This is the shape that previously broke: trade ids sit as top-level
	// siblings of success/method, exactly like the CoinTracking docs example.
	// time/imported_time are sent here as JSON strings (observed on the live
	// API) rather than the bare numbers shown in the docs example — FlexInt64
	// must accept both.
	srv := startMock(t, map[string]string{
		"getTrades": `{
			"success": 1,
			"method": "getTrades",
			"574025": {
				"buy_amount": "0.50000000",
				"buy_currency": "BTC",
				"sell_amount": "900.20000000",
				"sell_currency": "USD",
				"fee_amount": "4.50000000",
				"fee_currency": "USD",
				"type": "Trade",
				"exchange": "Kraken",
				"group": "",
				"comment": "This is a Kraken Trade",
				"imported_from": "kraken",
				"time": "1320442867",
				"imported_time": "1391037169",
				"trade_id": "43250"
			}
		}`,
	})

	got, err := mockClient(srv).GetTrades(context.Background(), TradesOptions{Order: "DESC", Limit: 50})
	if err != nil {
		t.Fatalf("GetTrades() error = %v", err)
	}
	trade, ok := got["574025"]
	if !ok {
		t.Fatalf("expected trade 574025, got %d trades", len(got))
	}
	if trade.BuyCurrency != "BTC" || trade.SellCurrency != "USD" {
		t.Errorf("trade currencies = buy:%q sell:%q, want BTC/USD", trade.BuyCurrency, trade.SellCurrency)
	}
	if trade.Exchange != "Kraken" {
		t.Errorf("trade.Exchange = %q, want Kraken", trade.Exchange)
	}
	if trade.Time != 1320442867 {
		t.Errorf("trade.Time = %d, want 1320442867", trade.Time)
	}
	if trade.ImportedTime != 1391037169 {
		t.Errorf("trade.ImportedTime = %d, want 1391037169", trade.ImportedTime)
	}
}

func TestGetTrades_BareNumberTimestamps(t *testing.T) {
	// The docs example shows time/imported_time as bare (unquoted) numbers;
	// FlexInt64 must accept this shape too, not just the quoted-string one.
	srv := startMock(t, map[string]string{
		"getTrades": `{
			"success": 1,
			"method": "getTrades",
			"574025": {
				"buy_amount": "0.50000000",
				"buy_currency": "BTC",
				"time": 1320442867,
				"imported_time": 1391037169,
				"trade_id": "43250"
			}
		}`,
	})

	got, err := mockClient(srv).GetTrades(context.Background(), TradesOptions{})
	if err != nil {
		t.Fatalf("GetTrades() error = %v", err)
	}
	if got["574025"].Time != 1320442867 {
		t.Errorf("trade.Time = %d, want 1320442867", got["574025"].Time)
	}
}

func TestCall_APIError(t *testing.T) {
	srv := startMock(t, map[string]string{
		"getBalance": `{"success":0,"method":"getBalance","error":"NO_METHOD","error_msg":"ERROR: Method not set"}`,
	})

	_, err := mockClient(srv).GetBalance(context.Background())
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "ERROR: Method not set") {
		t.Errorf("error = %v, want it to mention the api error_msg", err)
	}
}

func TestCall_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>upstream broke</html>"))
	}))
	t.Cleanup(srv.Close)

	_, err := mockClient(srv).Call(context.Background(), "getBalance", nil)
	if err == nil {
		t.Fatal("expected an error for a 502 response, got nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error = %v, want it to mention the HTTP status", err)
	}
}

func TestCall_ContextCancelled(t *testing.T) {
	srv := startMock(t, map[string]string{
		"getBalance": `{"success":1,"method":"getBalance","account_currency":"EUR","details":{},"summary":{"value_fiat":"0","value_btc":"0","profit_fiat":"0","profit_btc":"0"}}`,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call: it must not go through
	_, err := mockClient(srv).GetBalance(ctx)
	if err == nil {
		t.Fatal("expected an error from a cancelled context, got nil")
	}
}

func TestParseExtraParams(t *testing.T) {
	params, warnings := ParseExtraParams([]string{"price=best", "currency=BTC", "malformed"})
	if got := params.Get("price"); got != "best" {
		t.Errorf("price = %q, want best", got)
	}
	if got := params.Get("currency"); got != "BTC" {
		t.Errorf("currency = %q, want BTC", got)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"malformed"`) {
		t.Errorf("warnings = %v, want exactly one mentioning \"malformed\"", warnings)
	}
}

func TestParseExtraParams_ValueWithEquals(t *testing.T) {
	// Values containing '=' must survive: only the first '=' splits.
	params, warnings := ParseExtraParams([]string{"comment=a=b=c"})
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if got := params.Get("comment"); got != "a=b=c" {
		t.Errorf("comment = %q, want a=b=c", got)
	}
}

// sanity check that json.RawMessage round-trips cleanly through Call's
// field-stripping step (no leftover "success"/"method" keys).
func TestCall_StripsEnvelopeKeys(t *testing.T) {
	srv := startMock(t, map[string]string{
		"getBalance": `{"success":1,"method":"getBalance","account_currency":"EUR","details":{},"summary":{"value_fiat":"0","value_btc":"0","profit_fiat":"0","profit_btc":"0"}}`,
	})

	raw, err := mockClient(srv).Call(context.Background(), "getBalance", nil)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for _, k := range []string{"success", "method", "error", "error_msg"} {
		if _, present := fields[k]; present {
			t.Errorf("expected %q to be stripped from Call() result, found in %v", k, fields)
		}
	}
	if _, present := fields["account_currency"]; !present {
		t.Errorf("expected account_currency to survive stripping, got %v", fields)
	}
}

func TestNextNonce_StrictlyIncreasing(t *testing.T) {
	// Successive nonces must strictly increase even when generated faster
	// than the millisecond clock ticks — CoinTracking rejects reused nonces.
	prev := nextNonce()
	for i := 0; i < 10_000; i++ {
		n := nextNonce()
		if n <= prev {
			t.Fatalf("nonce did not increase: %d then %d (iteration %d)", prev, n, i)
		}
		prev = n
	}
}

func TestNonce_SentIncreasing(t *testing.T) {
	// End-to-end: two back-to-back calls must send strictly increasing
	// nonce form values.
	var nonces []int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		n, err := strconv.ParseInt(r.FormValue("nonce"), 10, 64)
		if err != nil {
			t.Errorf("bad nonce %q: %v", r.FormValue("nonce"), err)
		}
		nonces = append(nonces, n)
		_, _ = w.Write([]byte(`{"success":1,"method":"getBalance","account_currency":"EUR","details":{},"summary":{"value_fiat":"0","value_btc":"0","profit_fiat":"0","profit_btc":"0"}}`))
	}))
	t.Cleanup(srv.Close)

	c := mockClient(srv)
	for i := 0; i < 5; i++ {
		if _, err := c.GetBalance(context.Background()); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	for i := 1; i < len(nonces); i++ {
		if nonces[i] <= nonces[i-1] {
			t.Fatalf("nonces not strictly increasing: %v", nonces)
		}
	}
}
