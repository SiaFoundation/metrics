---
default: minor
---

# Robust handling of malformed v2 file contracts

`wellFormedV2Contract` now requires the full chain
`MissedHostValue ≤ TotalCollateral ≤ HostOutput.Value`. Consensus only
enforces the second inequality at formation (not on revisions), so a
non-standard revision that redistributes value from `HostOutput.Value`
to `RenterOutput.Value` can land the contract in a state where
`fc.RiskedHostRevenue()` or `HostOutput.Value − MissedHostValue`
underflows. The extended check catches both.

A new resolution type `ResolutionTypeAbandoned` is emitted when:

- An existing well-formed contract is revised into a malformed state, or
- A renewal produces a malformed new contract (where consensus
  validates the new contract but `MissedHostValue > TotalCollateral`
  is still allowed).

Abandoned contracts release their active counters (`ActiveContracts`,
`ActiveSize`, `LockedCollateral`, `RiskedCollateral`, `PotentialRevenue`,
locked allowance) using the last well-formed state as the basis. They do
not increment `SuccessfulContracts`, `FailedContracts`, or
`RenewedContracts` and don't touch `EarnedRevenue` or `BurntCollateral`
— no chain settlement has occurred. Lifetime stats (`TotalSize`,
`BytesUploaded`, `Tax`, `Spent`) are preserved.

Previously such contracts would either panic on `Currency.Sub`
underflow or stay counted as "active" indefinitely.
