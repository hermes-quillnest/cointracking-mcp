# cointracking-go

Minimal PoC Go client for the [CoinTracking API v1](https://cointracking.info/api/),
plus a CLI and an MCP (Model Context Protocol) server built on top of it.

## Layout

```
cointracking/     library package — the API client (Call, GetBalance, GetGroupedBalance, GetTrades, ...)
cmd/cli/          command-line tool (go run ./cmd/cli ...)
cmd/mcp/          MCP server over stdio, for Claude Code / Claude Desktop / any MCP client
```

## Setup

1. Create an API key at https://cointracking.info/settings/api (grant only the
   read permissions you need, e.g. "balance").
2. Export credentials:

   ```sh
   export COINTRACKING_KEY=your_key
   export COINTRACKING_SECRET=your_secret
   ```

## CLI

```sh
go run ./cmd/cli                    # defaults to getGroupedBalance
go run ./cmd/cli getBalance
go run ./cmd/cli getGroupedBalance
go run ./cmd/cli getTrades
go run ./cmd/cli getGains price=best        # falls through to the generic Call()
go run ./cmd/cli getHistoricalCurrency currency=BTC
```

`getBalance`/`getGroupedBalance`/`getTrades` use the typed convenience
methods; any other method name falls through to the generic, untyped
`Call()`, with `key=value` CLI args forwarded as extra request params.

Prints the pretty-printed JSON result from CoinTracking, or an error if the
call fails (bad signature, invalid method, rate limit, etc.).

Build a standalone binary with `go build -o bin/cointracking-cli ./cmd/cli`.

## MCP server

`cmd/mcp` exposes the same client as MCP tools over stdio:

| Tool | Wraps |
|---|---|
| `cointracking_get_balance` | `GetBalance()` |
| `cointracking_get_grouped_balance` | `GetGroupedBalance()` (group by exchange/group/type) |
| `cointracking_get_trades` | `GetTrades()` (limit, order, time range, trade prices) |
| `cointracking_call` | `Call()` — any other method (`getGains`, `getLedger`, `getHistoricalSummary`, `getTradesByUser`, ...) with raw `key=value` params |

Build it:

```sh
go build -o bin/cointracking-mcp ./cmd/mcp
```

### Register with Claude Code (local `claude` CLI)

```sh
claude mcp add cointracking \
  -e COINTRACKING_KEY=your_key \
  -e COINTRACKING_SECRET=your_secret \
  -- /absolute/path/to/cointracking-go/bin/cointracking-mcp
```

Use `-s user` to make it available in every project, or leave the default
(`local`, this project only). Verify with:

```sh
claude mcp list
claude mcp get cointracking
```

Then in a `claude` session just ask, e.g. "what's my CoinTracking balance?" —
Claude will call `cointracking_get_balance` automatically.

To remove it later: `claude mcp remove cointracking`.

### Manual smoke test (no Claude Code needed)

The server speaks stdio JSON-RPC, so any MCP client works, including the Go
SDK's own `CommandTransport` — connect, call `session.Tools(ctx, nil)` to
list tools, and `session.CallTool(ctx, ...)` to invoke one.

## API surface (`cointracking` package)

- `Client.Call(method string, params url.Values) (json.RawMessage, error)` —
  generic escape hatch for any CoinTracking method.
- `Client.GetBalance() (*BalanceResult, error)` — wraps `getBalance`.
- `Client.GetGroupedBalance(GroupedBalanceOptions) (*GroupedBalanceResult, error)` —
  wraps `getGroupedBalance` (group by exchange/group/type, optionally
  excluding deposits/withdrawals).
- `Client.GetTrades(TradesOptions) (map[string]Trade, error)` — wraps
  `getTrades` (limit, order, time range, optional trade prices).
- `ParseExtraParams([]string) (url.Values, []string)` — turns `"key=value"`
  strings into request params; used by both `cmd/cli` and `cmd/mcp`'s
  generic-call path.

All amount/value fields are kept as `string`, not `float64` — CoinTracking
returns arbitrary-precision decimals and round-tripping through float64 can
lose precision. Parse with `strconv.ParseFloat` or a decimal library as
needed.

`Trade.Time` / `Trade.ImportedTime` use a custom `FlexInt64` type: the live
API has been observed sending these as quoted strings (`"1320442867"`) even
though CoinTracking's own docs example shows bare numbers. `FlexInt64`
accepts either and behaves like a normal `int64` otherwise.

`COINTRACKING_BASE_URL` env var overrides the API endpoint the client posts
to — used for pointing at a mock/sandbox server during testing; unset in
normal use.

## Testing

`cointracking/cointracking_test.go` covers `Call()` and all three typed
methods against a mock HTTP server (`net/http/httptest`) seeded with
response bodies matching CoinTracking's actual documented JSON shape — no
live API key needed:

```sh
go test ./... -v
```

## Notes / limitations (PoC scope)

- Typed wrappers exist for `getBalance`, `getGroupedBalance`, and `getTrades`
  only; every other method (e.g. `getHistoricalSummary`, `getGains`,
  `getLedger`) still goes through the untyped `Call()` (both CLI and MCP
  expose it).
- Nonce is `unix millis`; if you fire requests faster than 1ms apart or reuse
  an old nonce, CoinTracking will reject the call ("nonce must be greater...").
- No rate-limit handling (CoinTracking limits calls per minute per key).
- No pagination helpers, no retry/backoff.
- The MCP server reads credentials once at startup from env vars; there's no
  per-call auth or multi-account support.
