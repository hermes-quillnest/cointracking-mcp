// Command cli is a quick command-line client for the CoinTracking API,
// mainly for ad-hoc testing of the same client the MCP server uses.
//
// Usage:
//
//	export COINTRACKING_KEY=xxxx
//	export COINTRACKING_SECRET=xxxx
//	go run ./cmd/cli getGroupedBalance   // typed
//	go run ./cmd/cli getBalance          // typed
//	go run ./cmd/cli getTrades           // typed
//	go run ./cmd/cli getGains price=best // falls through to untyped Call()
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	ct "cointracking-mcp/cointracking"
)

const usage = `usage: cointracking-cli [method] [key=value ...]

Typed methods (nice defaults, structured decoding):
  getBalance
  getGroupedBalance      (default when no method given)
  getTrades

Any other CoinTracking method is called generically, with key=value args
forwarded as request params, e.g.:
  cointracking-cli getGains price=best
  cointracking-cli getHistoricalCurrency currency=BTC

Requires COINTRACKING_KEY and COINTRACKING_SECRET env vars.
`

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		fmt.Print(usage)
		return
	}

	key := os.Getenv("COINTRACKING_KEY")
	secret := os.Getenv("COINTRACKING_SECRET")
	if key == "" || secret == "" {
		fmt.Fprintln(os.Stderr, "set COINTRACKING_KEY and COINTRACKING_SECRET env vars")
		os.Exit(1)
	}

	method := "getGroupedBalance"
	if len(os.Args) > 1 {
		method = os.Args[1]
	}

	extraParams, warnings := ct.ParseExtraParams(os.Args[2:])
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, w)
	}

	// Ctrl-C cancels the in-flight request instead of just killing the
	// process mid-read.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := ct.NewClient(key, secret)

	// Typed convenience methods have first-class subcommands; anything else
	// falls through to the generic, untyped Call() with whatever key=value
	// params were passed on the command line.
	var result any
	var err error
	switch method {
	case "getBalance":
		result, err = c.GetBalance(ctx)
	case "getGroupedBalance":
		result, err = c.GetGroupedBalance(ctx, ct.GroupedBalanceOptions{ExcludeDepWith: true})
	case "getTrades":
		result, err = c.GetTrades(ctx, ct.TradesOptions{Order: "DESC", Limit: 50})
	default:
		result, err = c.Call(ctx, method, extraParams)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: encode result:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
