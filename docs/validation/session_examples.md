# Micro-session fixtures

Hand-computed acceptance cases for the Go segment builder
(`backend/internal/segments/fixtures_test.go`). Written before serving tables
exist (FINAL_PLAN §9.2 / Phase 0).

| # | Fixture | Rule | Expected |
|---|---------|------|----------|
| 1 | Clean play → end | baseline | 1 segment |
| 2 | pause + keepalives + resume | R1 / D2 | 2 segments; pause excluded |
| 3 | BufferStart / BufferEnd | R2 / D3 | 1 unbroken segment |
| 4 | background + heartbeat + foreground | R3 | background heartbeat ignored |
| 5 | 5-minute heartbeat gap | R4 | cut at last_kp + 90s |
| 6 | sub-minute segment | R6 / R7 | contributes 1; does not vanish |
| 7 | crosses UTC hour + day | R9 | no truncation |
| 8 | end while paused / backgrounded | R1/R3/R5 | single closed segment |
| 9 | unmatched resume | asymmetry | no-op |
| 10 | VideoError then play | R5 | 2 segments |
| 11 | events after VideoSessionEnd | R5 terminal | ignored |
| 12 | two overlapping sessions / one user | §7.4 | session concurrency = 2 |
| 13 | open at watermark | R8 | clamped; no phantom tail |
| 14 | three sessions same minute | concurrency | peak = 3 |

Run:

```bash
cd backend && go test ./internal/segments/ ./internal/deltas/ ./internal/concurrency/ ./internal/preflight/
```
