---
default: minor
---

# Track on-chain activity and revision counts

Adds cumulative counters to support common product metrics:

- `Metrics.TransactionCount` (`v2_transaction_count` column) — total v2
  transactions across all indexed blocks. Daily on-chain activity can be
  derived by diffing two snapshots a day apart.
- `Metrics.RevisionCount`, `Host.RevisionCount`, `Renter.RevisionCount`
  (`revision_count` column) — cumulative count of v2 contract revision
  events at the global, host, and renter scope.

TVL is already trackable as `Metrics.LockedAllowance + Metrics.LockedCollateral`.
Daily Active Users (active renter and host keys) is already trackable via
`HostsCount(start, end)` and `RentersCount(start, end)` with day-boundary
arguments; both queries return the distinct count of keys that had any chain
event in the range.

Includes a database migration that adds the new columns, backfilled to zero.
The counters accumulate from the migration point forward.
