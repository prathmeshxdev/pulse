# Parameter sensitivity matrix (Layer 6)

Peak/avg concurrency under each locked semantic knob, over the full data window. Deltas are vs baseline — these are the percent-scale answer movers the design is judged on.

| Variant | Segments | Peak | Avg | ΔPeak | ΔAvg |
|---|--:|--:|--:|--:|--:|
| baseline (pause excluded, buffering active) | 148144 | 18968 | 0.56 | +0.0% | +0.0% |
| PAUSE_COUNTS_AS_ACTIVE = true (D2 flipped) | 98193 | 19587 | 0.58 | +3.3% | +3.4% |
| BUFFERING_COUNTS_AS_ACTIVE = false (D3 flipped) | 342565 | 22180 | 0.67 | +16.9% | +18.9% |

**Reading the D3 row.** Excluding buffering *raises* peak/avg because it fragments each buffered session into many short segments (see the segment-count jump); under any-overlap attribution every fragment rounds up to full-minute occupancy, so the boundary over-count outweighs the excluded stall time. Buffering is the dominant knob, and this fragmentation interaction is itself an argument for the locked D3 = true baseline (buffering active). D2 (pause) is a ~2% knob; the grace window is near-inert.
