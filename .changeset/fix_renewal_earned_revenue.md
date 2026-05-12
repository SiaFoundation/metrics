---
default: patch
---

# Fix host earned revenue and renter spend across v2 renewals and refreshes

`renewalRevenue` derived both the parent's revenue and the host's earned-now
amount from `parent.RiskedHostRevenue()` (= `parent.HostOutput.Value −
parent.TotalCollateral`). In v2, `HostOutput.Value` and `MissedHostValue` are
*mutable* across revisions, and revisions are off-chain by default — so the
on-chain parent reflects the last revision that was actually broadcast, which
is typically the formation. Any revenue the renter and host accrued via
off-chain revisions was invisible. The bug under-reported `HostEarnedRevenue`
on duration-extending renewals and either over- or under-reported the renter's
new ContractPrice on refreshes, depending on the staleness gap.

`TotalCollateral`, `ProofHeight`, `ExpirationHeight`, and the public keys are
the only consensus-immutable fields across v2 revisions. The latest off-chain
`HostOutput.Value` is recoverable from the resolution itself by the per-side
conservation convention rhp4 follows in `RenewContract`,
`RefreshContractPartialRollover`, and `RefreshContractFullRollover`:

```
latest.HostOutput.Value = renewal.FinalHostOutput.Value + renewal.HostRollover
```

Combined with the immutable `parent.TotalCollateral`, this gives the latest
`RiskedHostRevenue` directly. The fix reconstructs it that way:

```go
finalHostValue          := renewal.FinalHostOutput.Value.Add(renewal.HostRollover)
finalPotentialRevenue   := finalHostValue.Sub(parent.TotalCollateral)
newPotentialRevenue     := renewal.NewContract.RiskedHostRevenue()

if !renewalCarriesRevenue(parent, renewal.NewContract) {
    // duration-extending renewal: full latest revenue settles now
    return newPotentialRevenue, finalPotentialRevenue
}
// refresh: full latest revenue rolls forward; new ContractPrice = the delta
rolled := min(finalPotentialRevenue, newPotentialRevenue)
return newPotentialRevenue.Sub(rolled), finalPotentialRevenue.Sub(rolled)
```

The `renewalCarriesRevenue` gate (proof + expiration heights match) is kept
because the partial-refresh case where `rp.Collateral < latest.MissedHostValue`
produces `FinalHostOutput.Value > 0` from released *unrisked collateral* — not
earned revenue. The rhp4 path always rolls the entire `RiskedHostRevenue`
regardless, so the refresh branch can't be merged into a single uniform
formula on `HostRollover`.

Active-state release fields (`HostPotentialRevenue`, `HostRiskedCollateral`,
`RenterAllowance` on the resolution) continue to use on-chain parent values,
matching the running totals accumulated at formation and at any on-chain
revisions. Only the cumulative `HostEarnedRevenue` (resolution) and
`RenterContractPrice` (renewal-created formation) — the metrics that need to
reflect actual SC flow — are now derived from the resolution data.
