---
default: minor
---

# HTTP API expansion and cleanup

**New endpoints**

- `GET /metrics?start=&end=` — global metrics time series.
- `GET /hosts/:key/metrics?start=&end=` — per-host metrics time series.
  Routes the previously-unused `HostMetrics` interface method.
- `GET /renters/:key/metrics?start=&end=` — per-renter metrics time series.
  Routes the previously-unused `RenterMetrics` interface method.

All three default to the last 30 days truncated to the hour and validate
`end > start` (returning `400 Bad Request` otherwise).

**`UsageSummaryResponse` extended** with period deltas for the new
counters and an end-of-period TVL value:

```json
{
  "transactions": 0,
  "revisions": 0,
  "bytesUploaded": 0,
  "activeByteDays": 0,
  "hostEarnedRevenue": "0",
  "burntCollateral": "0",
  "tax": "0",
  "tvl": "0"
}
```

`/summary` also accepts optional `start` and `end` query parameters so
callers can align it with a custom `/metrics` window.

**Fixes**

- `NewHandler(chain, metrics)` now actually accepts the `Chain` it needs.
  Previously the `Chain` interface was declared but never wired in, so
  `GET /consensus/tip` would nil-deref on its first request.
- Dropped the unused `TipState()` method from the `Chain` interface.
- Removed three leftover stdlib `log.Println` debug statements from the
  delta handlers.
