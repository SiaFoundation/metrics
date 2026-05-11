# Sia Metrics

`metricd` is a small, self-contained indexer that follows the Sia blockchain
and aggregates v2 file contract activity into a SQLite database, exposing it
over an HTTP API. It is intended for network-health dashboards, exchange/token
reviews, and other consumers that need a derived view of on-chain Sia
storage activity without operating a wallet or hostd.

## What it tracks

All metrics are derived from v2 file contract diffs and v2 transactions in
each indexed block.

### Global (`Metrics`)

| Field | Meaning |
|---|---|
| `renters`, `hosts` | Distinct keys seen ever |
| `activeContracts` | Currently active v2 contracts |
| `renewedContracts`, `successfulContracts`, `failedContracts` | Lifetime resolution outcomes |
| `transactionCount` | Cumulative v2 transactions ever indexed |
| `revisionCount` | Cumulative v2 contract revision events |
| `activeSize` | Bytes currently stored across active contracts |
| `totalSize` | Lifetime chain-state size (grows on formation/grow-revisions, shrinks on shrink-revisions) |
| `bytesUploaded` | Monotonic cumulative bytes ever uploaded to the network |
| `activeByteDays` | Byte-days of storage (Riemann sum: `activeSize × blockInterval` per block) |
| `spentAllowance` | Cumulative SC paid by renters to hosts (contract price + storage payments) |
| `lockedAllowance` | Renter SC currently locked in active contracts |
| `tax` | Cumulative v2 file-contract tax (4% of contract value) paid to siafund holders |
| `potentialRevenue` | Host SC currently potentially earnable on proof |
| `earnedRevenue` | Host SC actually earned (via proof or renewal settlement) |
| `lockedCollateral` | Host SC currently locked as collateral |
| `riskedCollateral` | Subset of locked collateral subject to slashing on miss |
| `burntCollateral` | Total host SC burned to void on missed contracts |

### Per-host / per-renter (`Host`, `Renter`)

Same shape as the global counters, scoped to a single public key. Each
also carries:

- `firstSeen` — first block in which this key had any v2 contract event
- `lastActive` — most recent event block
- `revisionCount` — revisions involving this key

### Derived: TVL

There's no `tvl` field — sum `lockedAllowance + lockedCollateral` for the
v2 contract TVL at any snapshot.

### Derived: daily active / new users

- **DAU**: `HostsCount(start, end)` / `RentersCount(start, end)` return the
  distinct count of keys with any chain event in `[start, end]`. Pass day
  boundaries for daily granularity.
- **New per period**: `NewHosts(start, end)` / `NewRenters(start, end)`
  return distinct keys whose `firstSeen` falls in the range.

## Running

### From source

```sh
go build -o metricd ./cmd/metricd
./metricd -dir ./data -network mainnet
```

### Docker

```sh
docker run -p 9980:9980 -p 9981:9981 -v $PWD/data:/data \
    ghcr.io/siafoundation/metrics:latest
```

### Flags

| Flag | Default | Description |
|---|---|---|
| `-dir` | `.` | Data directory (consensus.db + metrics.sqlite3 live here) |
| `-network` | `mainnet` | Network: `mainnet` or `zen` |
| `-log.level` | `info` | Log level: `debug`, `info`, `warn`, `error` |

### Ports

- `:9980` — HTTP metrics API
- `:9981` — Sia syncer gateway

## HTTP API

Base URL: `http://127.0.0.1:9980/`. All responses are JSON.

| Method | Path | Returns |
|---|---|---|
| GET | `/consensus/tip` | `types.ChainIndex` of the indexed tip |
| GET | `/summary` | `UsageSummaryResponse` over the last 30 days |
| GET | `/metrics/last` | Latest global `Metrics` snapshot |
| GET | `/top/hosts` | Top 50 hosts by earned revenue (last 30 days) |
| GET | `/top/renters` | Top 50 renters by spent allowance (last 30 days) |
| GET | `/hosts/{key}/last` | Latest `Host` snapshot for the given public key |
| GET | `/renters/{key}/last` | Latest `Renter` snapshot for the given public key |
| GET | `/delta/{days}/metrics` | `[first, last]` global snapshots for diffing |
| GET | `/delta/{days}/hosts/{key}` | `[first, last]` per-host snapshots |
| GET | `/delta/{days}/renters/{key}` | `[first, last]` per-renter snapshots |

The `/delta/...` endpoints are intentionally cheap: each returns just two
snapshots so the client can compute the change in any cumulative counter
(transactions, revisions, bytes uploaded, earned revenue, tax, etc.) over
the period.

## Storage

A single SQLite database (`metrics.sqlite3`) holds three tables:

- `metrics` — global hourly snapshots
- `host_metrics` — per-host hourly snapshots
- `renter_metrics` — per-renter hourly snapshots

Snapshots collapse to one row per `(key, hour)` via `INSERT OR REPLACE`.
Schema migrations are versioned and run automatically on startup; see
[`persist/sqlite/init.sql`](persist/sqlite/init.sql) and
[`persist/sqlite/migrations.go`](persist/sqlite/migrations.go).

## Embedding

The indexer is also usable as a library:

```go
import (
    "go.sia.tech/metrics/metrics"
    "go.sia.tech/metrics/persist/sqlite"
)

store, _ := sqlite.OpenDatabase("metrics.sqlite3")
manager, _ := metrics.NewManager(chainManager, store, logger)
defer manager.Close()
```

The `Chain` interface ([metrics/metrics.go](metrics/metrics.go)) only requires the
methods `coreutils/chain.Manager` already implements (`TipState`,
`UpdatesSince`, `BestIndex`, `OnReorg`), so plugging it into an existing
daemon's chain manager is straightforward.

## Caveats

- **Non-standard contracts are skipped.** v2 contracts that don't fit the
  metric's payout decomposition (`MissedHostValue ≤ TotalCollateral ≤ HostOutput.Value`)
  are ignored — they can't be safely attributed to revenue vs. risked
  collateral. Contracts that transition to a malformed state mid-life are
  released from active counters as an "abandoned" resolution rather than
  left lingering.
- **`metricd` is its own chain manager.** It runs a full Sia syncer and
  builds its own consensus database — there's no `siad`/`hostd` to share
  with. Plan for tens of GB of consensus storage on mainnet.
- **No v1 contract tracking.** v1 is deprecated; only v2 file contracts
  are indexed.

## License

[MIT](LICENSE)
