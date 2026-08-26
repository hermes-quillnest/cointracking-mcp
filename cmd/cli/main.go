// Command cli is a quick PoC command-line client for the CoinTracking API.
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
	"encoding/json"
	"fmt"
	"os"

	ct "cointracking-mcp/cointracking"
)

func main() {
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

	c := ct.NewClient(key, secret)

	// Typed convenience methods have first-class subcommands; anything else
	// falls through to the generic, untyped Call() with whatever key=value
	// params were passed on the command line.
	var result any
	var err error
	switch method {
	case "getBalance":
		result, err = c.GetBalance()
	case "getGroupedBalance":
		result, err = c.GetGroupedBalance(ct.GroupedBalanceOptions{ExcludeDepWith: true})
	case "getTrades":
		result, err = c.GetTrades(ct.TradesOptions{Order: "DESC", Limit: 50})
	default:
		result, err = c.Call(method, extraParams)
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
