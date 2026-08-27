// Command cli is a quick command-line client for the CoinTracking API,
// mainly for ad-hoc testing of the same client the MCP server uses.
//
// Usage:
//
//	export COINTRACKING_KEY=xxxx
//	export COINTRACKING_SECRET=xxxx
//	go run ./cmd/cli getGroupedBalance                     // typed
//	go run ./cmd/cli getBalance                            // typed
//	go run ./cmd/cli getTrades                             // typed, defaults: DESC, limit=50
//	go run ./cmd/cli getTrades limit=0 order=ASC start=1514764800 end=1546300799
//	                                                        // typed, all params honored
//	go run ./cmd/cli getGains price=best                    // falls through to untyped Call()
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	ct "cointracking-mcp/pkg/cointracking"
)

const usage = `usage: cointracking-cli [method] [key=value ...]

Typed methods (nice defaults, structured decoding):
  getBalance
  getGroupedBalance      (default when no method given)
  getTrades              accepts limit, order, start, end, trade_prices
                         (limit=0 or omitted means "all"; order=ASC/DESC,
                         default DESC; start/end are UNIX timestamps;
                         trade_prices=1 includes buy/sell value in BTC/fiat)

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
		result, err = c.GetTrades(ctx, tradesOptionsFromParams(extraParams))
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

// tradesOptionsFromParams builds TradesOptions from the raw key=value CLI
// args for the "getTrades" subcommand. Without this, limit/order/start/end/
// trade_prices passed on the command line were silently ignored and every
// call fell back to the hardcoded {Order: "DESC", Limit: 50} default —
// always returning just the 50 most recent trades regardless of what was
// asked for. Malformed numeric values are reported to stderr and ignored
// (falling back to the field's zero value) rather than aborting the call.
func tradesOptionsFromParams(params url.Values) ct.TradesOptions {
	opts := ct.TradesOptions{
		Order: "DESC", // CoinTracking itself defaults to ASC; we keep the
		// CLI's previous default (most-recent-first) for anyone relying on
		// it, but every field below is now fully overridable.
		Limit: 50, // ditto: preserve the old no-args behavior (most recent
		// 50 trades) so a bare "getTrades" doesn't suddenly dump the full
		// account history. Pass limit=0 explicitly to get everything.
	}
	if v := params.Get("order"); v != "" {
		opts.Order = v
	}
	if v := params.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ignoring invalid limit=%q: %v\n", v, err)
		} else {
			opts.Limit = n
		}
	}
	if v := params.Get("start"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ignoring invalid start=%q: %v\n", v, err)
		} else {
			opts.Start = n
		}
	}
	if v := params.Get("end"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ignoring invalid end=%q: %v\n", v, err)
		} else {
			opts.End = n
		}
	}
	if v := params.Get("trade_prices"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ignoring invalid trade_prices=%q: %v\n", v, err)
		} else {
			opts.TradePrices = b
		}
	}
	return opts
}
