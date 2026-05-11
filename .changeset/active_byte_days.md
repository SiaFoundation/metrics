---
default: minor
---

# Track byte-days of network storage

New `ActiveByteDays` field on `Metrics` (and a corresponding `active_byte_days`
column). For each indexed block, `ActiveSize × blockInterval` (in days) is
added — a Riemann sum approximating the integral of stored bytes over time.

This is the protocol's "work done" unit: the underlying quantity that
host storage payouts are computed against. It's surfaced as a period
delta in `UsageSummaryResponse.activeByteDays`.

State now carries the per-block `BlockInterval` so the byte-days
accumulation is correct across networks with different block intervals
(mainnet vs zen). The block-level apply/revert is symmetric: the same
delta is added at the end of `ApplyState` and subtracted at the start of
`RevertState`.
