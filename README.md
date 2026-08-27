# cointracking-mcp

An MCP (Model Context Protocol) server that gives Claude (Code or Desktop)
access to your [CoinTracking](https://cointracking.info/) account, so you can
ask about your portfolio in plain English instead of calling the CoinTracking
API by hand. Built on a small Go client library, which also ships as a
standalone CLI.

## Setup

```sh
go build -o bin/cointracking-mcp ./cmd/mcp

claude mcp add cointracking \
  -e COINTRACKING_KEY=your_key \
  -e COINTRACKING_SECRET=your_secret \
  -- /absolute/path/to/cointracking-mcp/bin/cointracking-mcp
```

Get a key/secret at https://cointracking.info/settings/api — grant only the
read scopes you need. Use `-s user` to make the server available in every
project, or leave the default (`local`, this project only). Verify
registration with `claude mcp list` (or `claude mcp get cointracking` for
details).

To remove it later: `claude mcp remove cointracking`.

**Don't paste real keys/secrets directly on the command line if you can avoid
it** — they land in your shell history in plaintext (`~/.zsh_history` etc.).
Prefer sourcing them from an env file (`source .env.local && claude mcp add
... -e COINTRACKING_KEY=$COINTRACKING_KEY ...`) or a secrets manager. If you've
already done this, rotate the key at the URL above.

Registering the server also writes the key/secret in plaintext into Claude
Code's own project config (`.claude/settings.local.json` for `local` scope,
`~/.claude.json` for `user` scope) — `claude mcp get cointracking` prints
them right back at you. This repo's `.gitignore` excludes `.claude/`, but
double-check before committing if you've copied config around.

## What you can ask

Once registered, just talk to Claude naturally — it picks the right tool.
Examples that have actually been used against this server:

- "What's my CoinTracking balance?" → `cointracking_get_balance`
- "How many BTC did I have on CoinTracking one month ago?" → `cointracking_call`
  with `getHistoricalCurrency`
- "When and how much was the highest portfolio value in EUR?" → `cointracking_call`
  with `getHistoricalSummary`
- "And how much was that in BTC terms?" → derived by combining the above with
  the BTC/EUR price at that date (there's no single API field for "total
  value in BTC over time")
- "Show me my balance grouped by exchange" → `cointracking_get_grouped_balance`
- "What were my last 20 trades?" → `cointracking_get_trades`

## The four tools

| Tool | Use for |
|---|---|
| `cointracking_get_balance` | Current per-coin balance + account totals |
| `cointracking_get_grouped_balance` | Current balance split by exchange, group, or transaction type |
| `cointracking_get_trades` | Trade/transaction history (supports time range, limit, ordering) |
| `cointracking_call` | Anything else — pass a raw CoinTracking method name + params, e.g. `getHistoricalSummary`, `getHistoricalCurrency`, `getGains`, `getLedger`, `getTradesByUser` |

Reach for `cointracking_call` whenever a question involves *history* (past
balances, portfolio value over time) — there are no typed wrappers for those
methods yet.

## Known quirks (learned the hard way)

- **`getHistoricalCurrency` and `getHistoricalSummary` ignore date/currency
  filter params** and always return the *entire* daily history since account
  creation (thousands of entries). Expect a large JSON blob — Claude will
  save it to a file and query it with `jq` for the specific date/value you
  asked about rather than reading it all inline. This is normal, not a bug.
- **`getHistoricalSummary`'s `currency` param doesn't change the output
  currency either** — it's always denominated in your account's display
  currency (e.g. EUR). To answer "portfolio value in BTC," derive it: divide
  the EUR total by the BTC/EUR price at that timestamp (from
  `getHistoricalCurrency currency=BTC`'s `fiat`/`amount` ratio).
- **`getBalance` can look alarming when a portfolio is fully cashed out**: all
  crypto amounts show `0.00000000`, but `details` may still carry a
  non-zero fiat entry (e.g. `EUR`) — that's a genuine cash balance
  CoinTracking is tracking, not a display glitch. Don't confuse it with
  `summary.profit_fiat`/`profit_btc`, which is a completely separate field:
  the account's *lifetime realized P&L*, not a current holding. Both can be
  non-zero and non-equal at the same time — check the field name, not just
  "is there a number here."
- **Historical entries are daily snapshots**, not exact timestamps — a "N
  days/months ago" question resolves to the closest available snapshot, not
  the literal calendar date.
- **`getTrades` with `trade_prices=1` adds an `account_currency` sibling
  field** alongside the trade-id entries (observed in practice, not in the
  docs). `Client.GetTrades` strips it before decoding; if you're calling
  `getTrades` via the untyped `cointracking_call`/`Call()` instead, expect
  that extra key in the raw response.

## Troubleshooting

- **Tool call fails with `MCP error -32000: Connection closed`, or a tool
  suddenly reports `No such tool available`**: the server process died or the
  connection is flaky. Run `/mcp` in the Claude Code session to check status
  and reconnect; if that doesn't help, restart the session.
- **"set COINTRACKING_KEY and COINTRACKING_SECRET env vars before starting
  the server"**: the env vars weren't passed at registration — re-run
  `claude mcp add` with `-e` flags, or `claude mcp remove cointracking` first
  if it's already registered wrong.
- **Rebuilt the binary and changes aren't showing up?** `claude mcp add`
  points at the binary path, not its contents — rerun `go build -o
  bin/cointracking-mcp ./cmd/mcp`, then restart the Claude Code session (or
  `/mcp` reconnect) so it respawns the process with the new build. No need
  to re-register.
- **A call fails with a rate-limit-flavored error**: CoinTracking caps API
  keys at 20 calls/hour (PRO/EXPERT) or 60/hour (UNLIMITED). A session asking
  several history-heavy follow-ups can hit this — the tool surfaces
  CoinTracking's own error message when it does; just wait it out.
- To remove the server: `claude mcp remove cointracking`.

## Development

### Layout

```
pkg/cointracking/ library package — the API client (Call, GetBalance, GetGroupedBalance, GetTrades, ...)
cmd/cli/          command-line tool, mainly for ad-hoc testing (go run ./cmd/cli ...)
cmd/mcp/          the MCP server (see above)
```

### CLI

A thin wrapper around the same client, useful for testing calls without
going through Claude:

```sh
export COINTRACKING_KEY=your_key
export COINTRACKING_SECRET=your_secret

go run ./cmd/cli                    # defaults to getGroupedBalance
go run ./cmd/cli getBalance
go run ./cmd/cli getGroupedBalance
go run ./cmd/cli getTrades                          # defaults: order=DESC, limit=50
go run ./cmd/cli getTrades limit=0 order=ASC start=1514764800 end=1546300799
                                                     # limit/order/start/end/trade_prices
                                                     # are all forwarded now — limit=0 means "all"
go run ./cmd/cli getGains price=best        # falls through to the generic Call()
go run ./cmd/cli getHistoricalCurrency currency=BTC
```

`getBalance`/`getGroupedBalance`/`getTrades` use the typed convenience
methods; any other method name falls through to the generic, untyped
`Call()`, with `key=value` CLI args forwarded as extra request params.
Prints the pretty-printed JSON result from CoinTracking, or an error if the
call fails (bad signature, invalid method, rate limit, etc.).

Build a standalone binary with `go build -o bin/cointracking-cli ./cmd/cli`.

### API surface (`cointracking` package)

All methods take a `context.Context` (cancellation/timeouts propagate to the
HTTP request):

- `Client.Call(ctx, method string, params url.Values) (json.RawMessage, error)` —
  generic escape hatch for any CoinTracking method.
- `Client.GetBalance(ctx) (*BalanceResult, error)` — wraps `getBalance`.
- `Client.GetGroupedBalance(ctx, GroupedBalanceOptions) (*GroupedBalanceResult, error)` —
  wraps `getGroupedBalance` (group by exchange/group/type, optionally
  excluding deposits/withdrawals).
- `Client.GetTrades(ctx, TradesOptions) (map[string]Trade, error)` — wraps
  `getTrades` (limit, order, time range, optional trade prices).
- `ParseExtraParams([]string) (url.Values, []string)` — turns `"key=value"`
  strings into request params; used by `cmd/cli`'s generic-call path.

Nonces are generated from unix-millis through a process-wide monotonic
counter, so concurrent or rapid-fire calls can't reuse or reorder them.

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

### Testing

`pkg/cointracking/cointracking_test.go` covers `Call()` and all three typed
methods against a mock HTTP server (`net/http/httptest`) seeded with
response bodies matching CoinTracking's actual documented JSON shape — no
live API key needed:

```sh
go test ./... -v
```

### Notes / limitations (PoC scope)

- Typed wrappers exist for `getBalance`, `getGroupedBalance`, and `getTrades`
  only; every other method (e.g. `getHistoricalSummary`, `getGains`,
  `getLedger`) still goes through the untyped `Call()` (both CLI and MCP
  expose it).
- No rate-limit handling (CoinTracking limits calls per hour per key).
- No pagination helpers, no retry/backoff.
- The MCP server reads credentials once at startup from env vars; there's no
  per-call auth or multi-account support.
