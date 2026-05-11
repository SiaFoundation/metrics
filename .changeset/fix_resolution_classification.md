---
default: patch
---

# Fix contract success/failure classification on expiration

The check at expiration was inspecting the host's *cumulative*
`BurntCollateral` instead of *this* resolution's `HostBurn`:

```go
// before — wrong
if hm.BurntCollateral.IsZero() {
    hm.SuccessfulContracts++
    ...
}
```

Once a host had experienced any burn, every subsequent no-burn expiration
was misclassified as Failed — and the misclassification also propagated to
the renter and global counters for that contract. The check is now against
`resolution.HostBurn` so it reflects the per-contract outcome.
