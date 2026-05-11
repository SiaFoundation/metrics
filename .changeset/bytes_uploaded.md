---
default: minor
---

# Track monotonic bytes uploaded

New `BytesUploaded` field on `Host`, `Renter`, and `Metrics` (and a
`bytes_uploaded` column on each table). Unlike `TotalSize` — which mirrors
chain state and decreases on shrink revisions — `BytesUploaded` only ever
grows:

- formation: `+formation.Size`
- revision: `+max(0, NewSize − ExistingSize)`
- resolution: unchanged

Existing rows are backfilled to `total_size` as a best-effort lower bound;
the counter is exact from the migration point forward.

This captures the lifetime "data ever pushed to the network" number that
chain-state size doesn't.
