---
default: patch
---

# Fix unbounded global `ActiveSize` and renewal-inflated `BytesUploaded`

Three related size-tracking bugs in the sqlite metrics layer caused the
global `ActiveSize` to grow without bound and `BytesUploaded` to inflate
on every renewal:

1. **`ApplyState` resolution did not decrement `gm.ActiveSize`.**
   `hm.ActiveSize` and `rm.ActiveSize` were released correctly, but the
   global counter was never debited on expirations, proofs, renewals, or
   abandonments — so every fresh formation added to it permanently.
2. **`RevertState` formation did not decrement `gm.ActiveSize` or
   `gm.TotalSize`.** Symmetric to (1) on the revert side: the apply
   incremented both, the revert touched neither, so a reorg over a
   formation left both counters stuck.
3. **Renewal-created formations counted as fresh uploads.** A
   `V2FileContractRenewal` produces a resolution for the parent and a
   formation for the successor. The successor's `Filesize` is the
   parent's data carried forward, but the formation was incrementing
   `BytesUploaded` by that filesize. Since `BytesUploaded` is monotonic
   (the resolution does not debit it), every renewal inflated the
   counter by the contract's filesize. A long-renewing contract was
   counted N times.

`ContractFormation` now carries `FromRenewal bool`, set by `parseDiffs`
when the formation matches a renewal context. `Apply`/`RevertState`
honor the flag so renewal-created formations contribute zero to
`BytesUploaded`. `gm.ActiveSize` is now adjusted symmetrically with
`hm.ActiveSize`/`rm.ActiveSize` on every resolution and on revert of
every formation.
