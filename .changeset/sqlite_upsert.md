---
default: patch
---

# Use UPSERT for metric snapshot writes

`insertHostMetrics`, `insertRenterMetrics`, and `insertMetrics` now use
`INSERT … ON CONFLICT(...) DO UPDATE SET …` instead of
`INSERT OR REPLACE`. The previous behavior deleted and re-inserted the
row on conflict, which fires CHECK constraints from scratch and would
invalidate any future foreign-key references to the row. UPSERT updates
in place — same on-the-wire semantics for snapshots, but safer if the
schema grows constraints or referencing tables.
