// Command mcp-server exposes the CoinTracking API as an MCP (Model Context
// Protocol) server over stdio, for use with Claude Code, Claude Desktop, or
// any other MCP client.
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
	"os"

	ct "cointracking-go/cointracking"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var client *ct.Client

// --- getBalance ---------------------------------------------------------

type getBalanceInput struct{}

func handleGetBalance(_ context.Context, _ *mcp.CallToolRequest, _ getBalanceInput) (*mcp.CallToolResult, ct.BalanceResult, error) {
	res, err := client.GetBalance()
	if err != nil {
		// The SDK validates the typed output against its auto-generated
		// schema even on the error path, so the zero value must still be
		// schema-valid (non-nil maps, not "null").
		return errResult(err), ct.BalanceResult{Details: map[string]ct.CoinBalance{}}, nil
	}
	return nil, *res, nil
}

// --- getGroupedBalance ---------------------------------------------------

type getGroupedBalanceInput struct {
	Group          string `json:"group,omitempty" jsonschema:"group balance by 'exchange' (default), 'group', or 'type'"`
	ExcludeDepWith bool   `json:"exclude_dep_with,omitempty" jsonschema:"exclude deposits and withdrawals from the totals (recommended in most cases); defaults to true"`
	Type           string `json:"type,omitempty" jsonschema:"restrict to a single CoinTracking transaction type, e.g. 'Trade'"`
}

func handleGetGroupedBalance(_ context.Context, _ *mcp.CallToolRequest, in getGroupedBalanceInput) (*mcp.CallToolResult, ct.GroupedBalanceResult, error) {
	res, err := client.GetGroupedBalance(ct.GroupedBalanceOptions{
		Group:          in.Group,
		ExcludeDepWith: in.ExcludeDepWith,
		Type:           in.Type,
	})
	if err != nil {
		return errResult(err), ct.GroupedBalanceResult{Details: map[string]map[string]ct.GroupEntry{}}, nil
	}
	return nil, *res, nil
}

// --- getTrades -------------------------------------------------------------

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

func handleGetTrades(_ context.Context, _ *mcp.CallToolRequest, in getTradesInput) (*mcp.CallToolResult, getTradesOutput, error) {
	order := in.Order
	if order == "" {
		order = "DESC"
	}
	res, err := client.GetTrades(ct.TradesOptions{
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

// --- generic call ------------------------------------------------------

type callInput struct {
	Method string            `json:"method" jsonschema:"CoinTracking API method name, e.g. getGains, getHistoricalSummary, getHistoricalCurrency, getLedger, getTradesByUser"`
	Params map[string]string `json:"params,omitempty" jsonschema:"extra method-specific form params as key/value pairs, e.g. {\"price\":\"best\"} for getGains or {\"currency\":\"BTC\"} for getHistoricalCurrency"`
}

func handleCall(_ context.Context, _ *mcp.CallToolRequest, in callInput) (*mcp.CallToolResult, any, error) {
	if in.Method == "" {
		return errResult(fmt.Errorf("method is required")), nil, nil
	}
	params, warnings := ct.ParseExtraParams(paramsToArgs(in.Params))
	for _, w := range warnings {
		log.Println(w)
	}
	raw, err := client.Call(in.Method, params)
	if err != nil {
		return errResult(err), nil, nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return errResult(fmt.Errorf("decode response: %w", err)), nil, nil
	}
	return nil, out, nil
}

func paramsToArgs(params map[string]string) []string {
	args := make([]string, 0, len(params))
	for k, v := range params {
		args = append(args, k+"="+v)
	}
	return args
}

// errResult turns a Go error into a CallToolResult marked as an error, so
// MCP clients surface it as a tool failure instead of a crash.
func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}

func main() {
	key := os.Getenv("COINTRACKING_KEY")
	secret := os.Getenv("COINTRACKING_SECRET")
	if key == "" || secret == "" {
		log.Fatal("set COINTRACKING_KEY and COINTRACKING_SECRET env vars before starting the server")
	}
	client = ct.NewClient(key, secret)

	server := mcp.NewServer(&mcp.Implementation{Name: "cointracking", Version: "v0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "cointracking_get_balance",
		Description: "Get the current CoinTracking account and per-coin balance (amounts, fiat/BTC value, price trends).",
	}, handleGetBalance)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "cointracking_get_grouped_balance",
		Description: "Get the current balance grouped by exchange, trade-group, or transaction type.",
	}, handleGetGroupedBalance)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "cointracking_get_trades",
		Description: "Get CoinTracking trades and transactions (trade, deposit, withdrawal, income, etc.), most recent first by default.",
	}, handleGetTrades)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "cointracking_call",
		Description: "Call any CoinTracking API method directly (e.g. getGains, getHistoricalSummary, getHistoricalCurrency, getLedger, getTradesByUser) with raw key/value params. Use this for methods without a dedicated tool.",
	}, handleCall)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
