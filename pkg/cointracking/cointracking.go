// Package cointracking is a minimal client for the CoinTracking API
// (https://cointracking.info/api/).
//
// CoinTracking's v1 API is a single POST endpoint. Every request needs:
//   - a "method" form field (e.g. getBalance, getGroupedBalance, getTrades)
//   - a "nonce" form field that must strictly increase between requests
//   - a "Key" header with your API key
//   - a "Sign" header: hex(HMAC-SHA512(url-encoded form body, api_secret))
//
// Get an API key/secret at: https://cointracking.info/settings/api
package cointracking

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultAPIURL is the production CoinTracking API endpoint.
const DefaultAPIURL = "https://cointracking.info/api/v1/"

// maxResponseBytes caps how much of a response body we are willing to read.
// Historical endpoints legitimately return multiple MB; 64MB is far beyond
// anything sane and exists only to bound memory on a misbehaving server.
const maxResponseBytes = 64 << 20

// Client is a minimal CoinTracking API client. Construct it with NewClient.
type Client struct {
	// BaseURL is the endpoint Call() posts to. NewClient sets it to
	// DefaultAPIURL, or to $COINTRACKING_BASE_URL when that is non-empty
	// (useful for pointing at a mock/sandbox server).
	BaseURL string
	// HTTP is the underlying HTTP client. Override for custom timeouts or
	// transports.
	HTTP *http.Client

	key    string
	secret string
}

// NewClient builds a Client. The default HTTP timeout is generous because
// the historical endpoints (getHistoricalSummary, getHistoricalCurrency)
// return the full daily history of the account — often several MB.
func NewClient(key, secret string) *Client {
	base := DefaultAPIURL
	if v := os.Getenv("COINTRACKING_BASE_URL"); v != "" {
		base = v
	}
	return &Client{
		BaseURL: base,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
		key:     key,
		secret:  secret,
	}
}

// lastNonce backs nextNonce. Package-level (not per-client) on purpose:
// CoinTracking requires nonces to strictly increase per API key, and two
// Clients in one process may share a key.
var lastNonce atomic.Int64

// nextNonce returns unix-milliseconds, bumped as needed so that successive
// calls are strictly increasing even within the same millisecond.
func nextNonce() int64 {
	for {
		now := time.Now().UnixMilli()
		prev := lastNonce.Load()
		n := now
		if n <= prev {
			n = prev + 1
		}
		if lastNonce.CompareAndSwap(prev, n) {
			return n
		}
	}
}

// envelopeMeta captures the control fields CoinTracking mixes into every
// response object: {"success":1,"method":"...", ...data...} on success, or
// {"success":0,"method":"...","error":"CODE","error_msg":"..."} on failure.
// IMPORTANT: unlike many APIs, CoinTracking does NOT wrap the payload in a
// separate "return"/"data" field — the data fields sit flat, as siblings of
// success/method, directly in the top-level JSON object. Call() strips the
// control fields below and hands callers what remains.
type envelopeMeta struct {
	Success  int    `json:"success"`
	Method   string `json:"method"`
	Error    string `json:"error"`
	ErrorMsg string `json:"error_msg"`
}

// snippet renders a response body for inclusion in an error message without
// letting a huge (or hostile) body blow up the error string.
func snippet(b []byte) string {
	const max = 2048
	if len(b) <= max {
		return string(b)
	}
	return fmt.Sprintf("%s... (%d bytes total)", b[:max], len(b))
}

// Call invokes any CoinTracking API method and returns the data fields of the
// response (everything except success/method/error/error_msg) as a raw JSON
// object, ready to unmarshal into a method-specific struct or map.
// params holds extra method-specific fields (e.g. "coin", "limit"); pass nil for none.
func (c *Client) Call(ctx context.Context, method string, params url.Values) (json.RawMessage, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("method", method)
	params.Set("nonce", strconv.FormatInt(nextNonce(), 10))

	body := params.Encode()

	mac := hmac.New(sha512.New, []byte(c.secret))
	mac.Write([]byte(body))
	sign := hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Key", c.key)
	req.Header.Set("Sign", sign)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes, refusing to read further", maxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %s (body=%s)", resp.Status, snippet(raw))
	}

	var meta envelopeMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("decode response: %w (raw=%s)", err, snippet(raw))
	}
	if meta.Success != 1 {
		msg := meta.ErrorMsg
		if msg == "" {
			msg = meta.Error
		}
		return nil, fmt.Errorf("api error: %s (code=%s)", msg, meta.Error)
	}

	// Strip the envelope/control keys so callers get just the data fields,
	// still as a flat JSON object.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("decode response fields: %w (raw=%s)", err, snippet(raw))
	}
	delete(fields, "success")
	delete(fields, "method")
	delete(fields, "error")
	delete(fields, "error_msg")

	data, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("re-encode response fields: %w", err)
	}
	return data, nil
}

// --- Typed convenience methods ---------------------------------------------
//
// These wrap Call() for the three most commonly used CoinTracking methods and
// decode the JSON response fields into Go structs instead of raw JSON.
// Amounts/values are kept as strings (not float64): CoinTracking returns
// arbitrary-precision decimals and round-tripping through float64 can lose
// precision. Parse with strconv.ParseFloat (or a decimal lib) as needed.

// CoinBalance is one per-coin entry in BalanceResult.Details.
type CoinBalance struct {
	Amount    string `json:"amount"`
	Coin      string `json:"coin"`
	ValueFiat string `json:"value_fiat"`
	ValueBTC  string `json:"value_btc"`
	PriceFiat string `json:"price_fiat"`
	PriceBTC  string `json:"price_btc"`
	Change1h  string `json:"change1h"`
	Change24h string `json:"change24h"`
	Change7d  string `json:"change7d"`
	Change30d string `json:"change30d"`
}

// BalanceSummary is the account-wide totals block of BalanceResult.
type BalanceSummary struct {
	ValueFiat  string `json:"value_fiat"`
	ValueBTC   string `json:"value_btc"`
	ProfitFiat string `json:"profit_fiat"`
	ProfitBTC  string `json:"profit_btc"`
}

// BalanceResult is the typed response payload of the getBalance method.
type BalanceResult struct {
	AccountCurrency string                 `json:"account_currency"`
	Details         map[string]CoinBalance `json:"details"`
	Summary         BalanceSummary         `json:"summary"`
}

// GetBalance calls getBalance (no params) and decodes it into a BalanceResult.
// Docs: https://cointracking.info/api/api.php#getBalance
func (c *Client) GetBalance(ctx context.Context) (*BalanceResult, error) {
	raw, err := c.Call(ctx, "getBalance", nil)
	if err != nil {
		return nil, err
	}
	var out BalanceResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode getBalance: %w", err)
	}
	return &out, nil
}

// GroupEntry is one coin's amount/value within a getGroupedBalance group.
// Each group also carries a synthetic "TOTAL_SUMMARY" entry that only sets
// Fiat/BTC (Amount is empty there).
type GroupEntry struct {
	Amount string `json:"amount,omitempty"`
	Fiat   string `json:"fiat"`
	BTC    string `json:"btc"`
}

// GroupedBalanceResult is the typed response payload of getGroupedBalance.
// Details is keyed by group name (e.g. exchange name), then by coin symbol
// (plus the "TOTAL_SUMMARY" pseudo-coin per group).
type GroupedBalanceResult struct {
	AccountCurrency string                           `json:"account_currency"`
	Details         map[string]map[string]GroupEntry `json:"details"`
}

// GroupedBalanceOptions are the optional parameters for GetGroupedBalance.
type GroupedBalanceOptions struct {
	Group          string // "exchange" (default), "group", or "type"
	ExcludeDepWith bool   // exclude deposits/withdrawals (recommended in most cases)
	Type           string // restrict to one transaction type, e.g. "Trade"
}

// GetGroupedBalance calls getGroupedBalance and decodes it into a typed result.
// Docs: https://cointracking.info/api/api.php#getGroupedBalance
func (c *Client) GetGroupedBalance(ctx context.Context, opts GroupedBalanceOptions) (*GroupedBalanceResult, error) {
	params := url.Values{}
	if opts.Group != "" {
		params.Set("group", opts.Group)
	}
	if opts.ExcludeDepWith {
		params.Set("exclude_dep_with", "1")
	}
	if opts.Type != "" {
		params.Set("type", opts.Type)
	}
	raw, err := c.Call(ctx, "getGroupedBalance", params)
	if err != nil {
		return nil, err
	}
	var out GroupedBalanceResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode getGroupedBalance: %w", err)
	}
	return &out, nil
}

// FlexInt64 decodes a JSON int64 that CoinTracking sometimes sends as a bare
// number and sometimes as a quoted string (observed in practice on "time"/
// "imported_time" fields, despite the docs showing bare numbers). It behaves
// like a plain int64 everywhere except unmarshalling.
type FlexInt64 int64

func (f *FlexInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("FlexInt64: %w", err)
	}
	*f = FlexInt64(n)
	return nil
}

// Trade is one entry in GetTrades's result, keyed by CoinTracking trade id.
type Trade struct {
	BuyAmount    string    `json:"buy_amount"`
	BuyCurrency  string    `json:"buy_currency"`
	SellAmount   string    `json:"sell_amount"`
	SellCurrency string    `json:"sell_currency"`
	FeeAmount    string    `json:"fee_amount"`
	FeeCurrency  string    `json:"fee_currency"`
	Type         string    `json:"type"`
	Exchange     string    `json:"exchange"`
	Group        string    `json:"group"`
	Comment      string    `json:"comment"`
	ImportedFrom string    `json:"imported_from"`
	Time         FlexInt64 `json:"time"`
	ImportedTime FlexInt64 `json:"imported_time"`
	TradeID      string    `json:"trade_id"`
}

// TradesOptions are the optional parameters for GetTrades.
type TradesOptions struct {
	Limit       int    // 0 = all
	Order       string // "ASC" (default) or "DESC"
	Start, End  int64  // UNIX timestamps; 0 = no bound
	TradePrices bool   // include buy/sell value in BTC + account currency
}

// GetTrades calls getTrades and decodes it into a map of trade id -> Trade.
// Docs: https://cointracking.info/api/api.php#getTrades
//
// When TradesOptions.TradePrices is set, CoinTracking adds an
// "account_currency" field as a sibling of the trade-id entries (observed in
// practice; not documented). That single extra key made a naive
// map[string]Trade unmarshal fail outright, so it's stripped first — same
// pattern Call() already uses for the envelope/control fields.
func (c *Client) GetTrades(ctx context.Context, opts TradesOptions) (map[string]Trade, error) {
	params := url.Values{}
	if opts.Limit > 0 {
		params.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Order != "" {
		params.Set("order", opts.Order)
	}
	if opts.Start > 0 {
		params.Set("start", strconv.FormatInt(opts.Start, 10))
	}
	if opts.End > 0 {
		params.Set("end", strconv.FormatInt(opts.End, 10))
	}
	if opts.TradePrices {
		params.Set("trade_prices", "1")
	}
	raw, err := c.Call(ctx, "getTrades", params)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("decode getTrades: %w", err)
	}
	delete(fields, "account_currency")
	out := make(map[string]Trade, len(fields))
	for id, r := range fields {
		var t Trade
		if err := json.Unmarshal(r, &t); err != nil {
			return nil, fmt.Errorf("decode getTrades: trade %s: %w", id, err)
		}
		out[id] = t
	}
	return out, nil
}

// ParseExtraParams turns "key=value" strings into url.Values, used to pass
// method-specific parameters through to the untyped Call() (e.g. "price=best"
// for getGains, or "currency=BTC" for getHistoricalCurrency). Entries without
// an "=" are skipped and reported back as warning strings rather than
// aborting.
func ParseExtraParams(args []string) (url.Values, []string) {
	params := url.Values{}
	var warnings []string
	for _, arg := range args {
		k, v, ok := strings.Cut(arg, "=")
		if !ok {
			warnings = append(warnings, fmt.Sprintf("ignoring malformed param %q (want key=value)", arg))
			continue
		}
		params.Set(k, v)
	}
	return params, warnings
}
