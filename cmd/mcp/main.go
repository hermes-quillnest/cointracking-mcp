// Command cointracking-mcp exposes the CoinTracking API as an MCP (Model
// Context Protocol) server over stdio, for use with Claude Code, Claude
// Desktop, or any other MCP client.
//
// Auth is read once at startup from COINTRACKING_KEY / COINTRACKING_SECRET
// env vars (set them wherever you register the server, e.g. `claude mcp add`).
//
// Tools exposed:
//   - cointracking_get_balance          -> Client.GetBalance
//   - cointracking_get_grouped_balance  -> Client.GetGroupedBalance
//   - cointracking_get_trades           -> Client.GetTrades
//   - cointracking_call                 -> Client.Call (any method, raw params)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	ct "cointracking-mcp/pkg/cointracking"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const serverVersion = "v0.2.0"

// server bundles the API client with the tool handlers, so handlers don't
// reach for package-level globals.
type server struct {
	client *ct.Client
}

// --- getBalance -------------------------------------------------------------

type getBalanceInput struct{}

func (s *server) handleGetBalance(ctx context.Context, _ *mcp.CallToolRequest, _ getBalanceInput) (*mcp.CallToolResult, ct.BalanceResult, error) {
	res, err := s.client.GetBalance(ctx)
	if err != nil {
		// The SDK validates the typed output against its auto-generated
		// schema even on the error path, so the zero value must still be
		// schema-valid (non-nil maps, not "null").
		return errResult(err), ct.BalanceResult{Details: map[string]ct.CoinBalance{}}, nil
	}
	return nil, *res, nil
}

// --- getGroupedBalance ------------------------------------------------------

type getGroupedBalanceInput struct {
	Group          string `json:"group,omitempty" jsonschema:"group balance by 'exchange' (default), 'group', or 'type'"`
	ExcludeDepWith *bool  `json:"exclude_dep_with,omitempty" jsonschema:"exclude deposits and withdrawals from the totals; defaults to true (recommended), pass false to include them"`
	Type           string `json:"type,omitempty" jsonschema:"restrict to a single CoinTracking transaction type, e.g. 'Trade'"`
}

func (s *server) handleGetGroupedBalance(ctx context.Context, _ *mcp.CallToolRequest, in getGroupedBalanceInput) (*mcp.CallToolResult, ct.GroupedBalanceResult, error) {
	// The schema documents exclude_dep_with as defaulting to true, so an
	// omitted field must actually mean true — hence *bool, not bool (whose
	// zero value would silently flip the default to false).
	excludeDepWith := true
	if in.ExcludeDepWith != nil {
		excludeDepWith = *in.ExcludeDepWith
	}
	res, err := s.client.GetGroupedBalance(ctx, ct.GroupedBalanceOptions{
		Group:          in.Group,
		ExcludeDepWith: excludeDepWith,
		Type:           in.Type,
	})
	if err != nil {
		return errResult(err), ct.GroupedBalanceResult{Details: map[string]map[string]ct.GroupEntry{}}, nil
	}
	return nil, *res, nil
}

// --- getTrades --------------------------------------------------------------

type getTradesInput struct {
	Limit       int    `json:"limit,omitempty" jsonschema:"max number of trades to return; 0 means all"`
	Order       string `json:"order,omitempty" jsonschema:"'ASC' or 'DESC' order by trade time; defaults to DESC"`
	Start       int64  `json:"start,omitempty" jsonschema:"UNIX timestamp lower bound (inclusive); 0 means no bound"`
	End         int64  `json:"end,omitempty" jsonschema:"UNIX timestamp upper bound (inclusive); 0 means no bound"`
	TradePrices bool   `json:"trade_prices,omitempty" jsonschema:"include buy/sell value in BTC and account currency"`
}

type getTradesOutput struct {
	Trades map[string]ct.Trade `json:"trades"`
}

func (s *server) handleGetTrades(ctx context.Context, _ *mcp.CallToolRequest, in getTradesInput) (*mcp.CallToolResult, getTradesOutput, error) {
	order := in.Order
	if order == "" {
		order = "DESC"
	}
	res, err := s.client.GetTrades(ctx, ct.TradesOptions{
		Limit:       in.Limit,
		Order:       order,
		Start:       in.Start,
		End:         in.End,
		TradePrices: in.TradePrices,
	})
	if err != nil {
		return errResult(err), getTradesOutput{Trades: map[string]ct.Trade{}}, nil
	}
	return nil, getTradesOutput{Trades: res}, nil
}

// --- generic call -----------------------------------------------------------

type callInput struct {
	Method string            `json:"method" jsonschema:"CoinTracking API method name, e.g. getGains, getHistoricalSummary, getHistoricalCurrency, getLedger, getTradesByUser"`
	Params map[string]string `json:"params,omitempty" jsonschema:"extra method-specific form params as key/value pairs, e.g. {\"price\":\"best\"} for getGains or {\"currency\":\"BTC\"} for getHistoricalCurrency"`
}

func (s *server) handleCall(ctx context.Context, _ *mcp.CallToolRequest, in callInput) (*mcp.CallToolResult, any, error) {
	if in.Method == "" {
		return errResult(fmt.Errorf("method is required")), nil, nil
	}
	// Build url.Values directly from the map — no "k=v" string round-trip,
	// which would mangle keys or values containing '='.
	params := url.Values{}
	for k, v := range in.Params {
		params.Set(k, v)
	}
	raw, err := s.client.Call(ctx, in.Method, params)
	if err != nil {
		return errResult(err), nil, nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return errResult(fmt.Errorf("decode response: %w", err)), nil, nil
	}
	return nil, out, nil
}

// errResult turns a Go error into a CallToolResult marked as an error, so
// MCP clients surface it as a tool failure instead of a crash.
func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}

// readOnly marks a tool as non-mutating: every CoinTracking API method is a
// read; nothing this server does can alter the account.
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:        title,
		ReadOnlyHint: true,
	}
}

func newMCPServer(client *ct.Client) *mcp.Server {
	s := &server{client: client}

	srv := mcp.NewServer(&mcp.Implementation{Name: "cointracking", Version: serverVersion}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cointracking_get_balance",
		Description: "Get the current CoinTracking account and per-coin balance (amounts, fiat/BTC value, price trends).",
		Annotations: readOnly("Get balance"),
	}, s.handleGetBalance)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cointracking_get_grouped_balance",
		Description: "Get the current balance grouped by exchange, trade-group, or transaction type.",
		Annotations: readOnly("Get grouped balance"),
	}, s.handleGetGroupedBalance)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cointracking_get_trades",
		Description: "Get CoinTracking trades and transactions (trade, deposit, withdrawal, income, etc.), most recent first by default.",
		Annotations: readOnly("Get trades"),
	}, s.handleGetTrades)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cointracking_call",
		Description: "Call any CoinTracking API method directly (e.g. getGains, getHistoricalSummary, getHistoricalCurrency, getLedger, getTradesByUser) with raw key/value params. Use this for methods without a dedicated tool. Note: getHistoricalSummary and getHistoricalCurrency ignore date filters and return the full daily history — expect a large response.",
		Annotations: readOnly("Call any CoinTracking method"),
	}, s.handleCall)

	return srv
}

func main() {
	log.SetFlags(0) // stderr goes to the MCP client's log; timestamps are added there

	key := os.Getenv("COINTRACKING_KEY")
	secret := os.Getenv("COINTRACKING_SECRET")
	if key == "" || secret == "" {
		log.Fatal("set COINTRACKING_KEY and COINTRACKING_SECRET env vars before starting the server")
	}

	// Shut down cleanly when the MCP client sends SIGINT/SIGTERM (or the
	// terminal closes); in-flight HTTP requests are cancelled via ctx.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := newMCPServer(ct.NewClient(key, secret))
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		log.Fatalf("server failed: %v", err)
	}
}
