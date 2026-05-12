---
default: patch
---

# Fix renter `Spent` falling behind host `EarnedRevenue` across renewals

`rm.Spent` and `gm.SpentAllowance` were credited only at formation
(ContractPrice) and on-chain revisions (allowance delta). For any contract
that accrued revenue via off-chain revisions — i.e. essentially every v2
contract — that accrual was never visible to the renter side, even though
the corresponding host `EarnedRevenue` *was* captured at the eventual
renewal (via the resolution-derived `renewalRevenue` fix). The two
counters drifted apart by the network's total off-chain accrual.

`ContractResolution.RenterDeferredSpend` carries the per-renewal gap
between the latest off-chain `RiskedHostRevenue` (reconstructed from
`FinalHostOutput.Value + HostRollover − parent.TotalCollateral`) and the
on-chain `parent.RiskedHostRevenue()`. The sqlite apply path credits it
to `rm.Spent` and `gm.SpentAllowance`; revert subtracts symmetrically.
Clamped to zero in the unusual case where an off-chain refund left
latest below on-chain.

After the fix, `Σ rm.Spent = Σ hm.EarnedRevenue` across any closed
renewal lineage. The timing of the credit doesn't perfectly match when
the SC actually left the renter's wallet (it's deferred to the renewal
moment), but the magnitude is correct.
