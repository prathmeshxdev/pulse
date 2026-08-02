# ClickStack / HyperDX evidence exports

CSV exports from **HyperDX** (ClickStack UI) search results, captured 2026-08-02
during dashboard and API smoke testing against `pulse-concurrency-api`.

| File | Signal | Columns |
|------|--------|---------|
| [`hyperdx_logs_2026-08-02.csv`](hyperdx_logs_2026-08-02.csv) | Logs | Timestamp, service, level, Body |
| [`hyperdx_traces_2026-08-02.csv`](hyperdx_traces_2026-08-02.csv) | Traces | Timestamp, service, level, duration, SpanName |

Screenshots of the same views: [`../screenshots/`](../screenshots/)
(`clickstack-logs.png`, `clickstack-traces.png`).

Original export filenames:
`hyperdx_search_results_2026-08-02T05-18-14.csv.csv` (logs),
`hyperdx_search_results_2026-08-02T05-18-17.csv.csv` (traces).
