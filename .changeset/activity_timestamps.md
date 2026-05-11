---
default: minor
---

# Track per-key activity timestamps and new-user counts

`Host` and `Renter` gain `firstSeen` / `lastActive` timestamps, populated
on every contract event for that key. Existing rows are backfilled to the
min/max of their snapshot `date_created` via a migration.

Two new store methods derive "new in period" counts from `firstSeen`:

```go
NewHosts(ctx, start, end)   // distinct hosts whose firstSeen is in [start, end]
NewRenters(ctx, start, end) // distinct renters whose firstSeen is in [start, end]
```

These are surfaced in `UsageSummaryResponse` as `newHosts` and `newRenters`.
Daily granularity is achieved by passing day-boundary `start`/`end` values.
