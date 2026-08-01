# Incremental / open-session evidence (truncate-and-replay)

Proves the serving layer absorbs late events **without a full rebuild**, and that
the incremental path reproduces the full batch build exactly.

Method: split the raw CSV at a watermark (last 5 min held back), build segments
from the pre-watermark data only, benchmark (**before.json**), then append the
tail and run `reconcile` on the affected sessions, benchmark again (**after.json**).

| | segments | unfiltered avg | notes |
|---|--:|--:|---|
| Full batch build | 32,291 | 8.152 | reference |
| Before (pre-watermark only) | 31,413 | 8.020 | 1,221 sessions still open, clamped to watermark |
| After reconcile (incremental) | **32,291** | **8.152** | 1,221 sessions reconciled: 4,014 new segments, 6,272 published edges cancelled, 8,028 new deltas — **no rebuild** |

Reconcile result == full batch build: identical segment count and identical
peak/avg across every benchmark case. All 6 Layer-5 invariants still pass on the
reconciled state.
