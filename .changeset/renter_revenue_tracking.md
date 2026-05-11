---
default: minor
---

# Track renter contract price and siafund tax

`Renter.Spent` and `Metrics.SpentAllowance` now include the contract price paid
upfront to the host at each formation (`HostOutput.Value - TotalCollateral`),
in addition to the allowance consumed by storage revisions. Previously the
contract price was silently dropped, so renter spend could not be reconciled
against host `EarnedRevenue`.

A new `Tax` field on `Renter` and `Metrics` tracks the cumulative v2 file
contract tax (4% of `RenterOutput + HostOutput`) paid to siafund holders at
each formation/renewal. Tax is reported separately from `Spent` because it
does not flow to the host.

Includes a database migration that adds the `tax` column to the
`renter_metrics` and `metrics` tables, backfilled to zero.

Also guards against the underflow panic in `parseDiffs` when indexing a
consensus-valid v2 file contract whose `MissedHostValue > TotalCollateral`
(the metric model doesn't apply to such contracts; they are now skipped with
a warning), and removes a dead branch that tried to compute `RenterSpent` at
renewal resolutions — that spend is captured by the per-revision allowance
deltas and the new formation's `RenterContractPrice`.
