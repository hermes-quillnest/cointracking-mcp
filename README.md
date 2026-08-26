# cointracking-go

Minimal PoC Go client for the [CoinTracking API v1](https://cointracking.info/api/).

## Setup

1. Create an API key at https://cointracking.info/settings/api (grant only the
   read permissions you need, e.g. "balance").
2. Export credentials:

   ```sh
   export COINTRACKING_KEY=your_key
   export COINTRACKING_SECRET=your_secret
   ```

## Run

```sh
go run .                    # defaults to getGroupedBalance
go run . getBalance
go run . getGroupedBalance
go run . getTrades
```

The three subcommands above use the typed convenience methods; any other
method name falls through to the generic, untyped `Call()`.

Prints the pretty-printed JSON result from CoinTracking, or an error if the
call fails (bad signature, invalid method, rate limit, etc.).

## API surface

- `Client.Call(method string, params url.Values) (json.RawMessage, error)` —
  generic escape hatch for any CoinTracking method.
- `Client.GetBalance() (*BalanceResult, error)` — wraps `getBalance`.
- `Client.GetGroupedBalance(GroupedBalanceOptions) (*GroupedBalanceResult, error)` —
  wraps `getGroupedBalance` (group by exchange/group/type, optionally
  excluding deposits/withdrawals).
- `Client.GetTrades(TradesOptions) (map[string]Trade, error)` — wraps
  `getTrades` (limit, order, time range, optional trade prices).

All amount/value fields are kept as `string`, not `float64` — CoinTracking
returns arbitrary-precision decimals and round-tripping through float64 can
lose precision. Parse with `strconv.ParseFloat` or a decimal library as
needed.

`Trade.Time` / `Trade.ImportedTime` use a custom `FlexInt64` type: the live
API has been observed sending these as quoted strings (`"1320442867"`) even
though CoinTracking's own docs example shows bare numbers. `FlexInt64`
accepts either and behaves like a normal `int64` otherwise.

## Testing

`main_test.go` covers `Call()` and all three typed methods against a mock
HTTP server (`net/http/httptest`) seeded with response bodies matching
CoinTracking's actual documented JSON shape — no live API key needed:

```sh
go test ./... -v
```

## Notes / limitations (PoC scope)

- Typed wrappers exist for `getBalance`, `getGroupedBalance`, and `getTrades`
  only; every other method (e.g. `getHistoricalSummary`, `getGains`,
  `getLedger`) still goes through the untyped `Call()`.
- Nonce is `unix millis`; if you fire requests faster than 1ms apart or reuse
  an old nonce, CoinTracking will reject the call ("nonce must be greater...").
- No rate-limit handling (CoinTracking limits calls per minute per key).
- No pagination helpers, no retry/backoff.
