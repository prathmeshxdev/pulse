# Sony LIV Foreground-Only Concurrency

Click-a-thon 2026 solution: foreground-only concurrent-viewer analytics on ClickHouse, at minute, hour, and day grain, with arbitrary dimension filters.

**The design position, stated up front.** At ~10,866 sessions and ~10⁵ delta rows, every team's queries will be fast, so measured latency cannot be the differentiator. This solution competes on **defensible semantics and reproducible correctness**: what "actively watching" means, why paused playback is excluded while buffering is not, and why each benchmark answer is the one a viewer of the concurrency curve would read off it. The scaling argument is made analytically for 100× rather than by benchmarking a dataset that fits in cache.

## Start here

Read in this order. Two documents are authoritative and the rest are supporting detail.

| # | Doc | Role | Read it when |
|---|-----|------|--------------|
| 1 | **[docs/FINAL_PLAN.md](docs/FINAL_PLAN.md)** | **Authoritative.** §1 is the complete locked rule set (R1–R10); §16 is the single list of open questions | Always. This is the build plan and the tie-breaker |
| 2 | **[docs/SEMANTICS_SPEC.md](docs/SEMANTICS_SPEC.md)** | **Decision record.** The four binding decisions with rationale, plus what changed from earlier drafts and why | You want to know *why* a rule is what it is, or you are tempted to change one |
| 3 | [docs/DATA_AND_PROBLEM_UNDERSTANDING.md](docs/DATA_AND_PROBLEM_UNDERSTANDING.md) | The problem, the datasets, and the measured data profile | You are new to the problem or need the event taxonomy |
| 4 | [docs/ACTIVE_INTERVAL_LOGIC.md](docs/ACTIVE_INTERVAL_LOGIC.md) | How `session_active_segments` is derived from raw events | You are implementing or debugging the segment builder |
| 5 | [docs/SCHEMA_AND_DDL.md](docs/SCHEMA_AND_DDL.md) | Table DDL, dictionary, idempotency mechanisms, schema evolution, migration map | You are writing migrations or reasoning about physical design |
| 6 | [docs/VALIDATION.md](docs/VALIDATION.md) | Validation layers, invariants, sensitivity matrix, evidence artifacts | You are proving the pipeline correct |
| 7 | [docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md) | Phasing, product-layer scope, integration surface | You are planning work or sequencing |

### Which document wins

`FINAL_PLAN.md` §1 is the rule set to implement against. `SEMANTICS_SPEC.md` records the same decisions with fuller rationale and carries the history of what was tried and rejected. **They must never disagree**, and `SEMANTICS_SPEC.md` §0 holds the mapping between them. If any other document contradicts either, the other document is the bug.

## For a judge, in five minutes

| Question | Where it is answered |
|---|---|
| What counts as "actively watching"? | [FINAL_PLAN.md §1.4](docs/FINAL_PLAN.md#14-the-active-predicate) — four conditions, all required |
| Why is paused playback excluded but buffering included? | [FINAL_PLAN.md R1 and R2](docs/FINAL_PLAN.md#15-the-rules-with-rationale). The asymmetry is deliberate and argued from viewer intent |
| How are peak and average defined, exactly? | [FINAL_PLAN.md §2](docs/FINAL_PLAN.md#2-metric-definitions). One primitive: the filtered minute curve |
| Why is the answer trustworthy without an answer key? | [VALIDATION.md](docs/VALIDATION.md) — hand-computed fixtures, automated invariants, an independent Python reference, and a published sensitivity matrix |
| Does it scale, and where does it break? | [FINAL_PLAN.md §15](docs/FINAL_PLAN.md#15-scaling-to-100), including §15.10 on genuine weaknesses as distinct from managed trade-offs |
| What is deliberately not built? | [FINAL_PLAN.md §12](docs/FINAL_PLAN.md#12-what-we-are-deliberately-not-building) |

## Problem source

- [SonyLIV problem statement](https://github.com/sidagarwal04/click-a-thon-2026/blob/main/SonyLiv/PROBLEM_STATEMENT.md)
- [Dataset details](https://github.com/sidagarwal04/click-a-thon-2026/blob/main/SonyLiv/dataset_details.md)

## Repo layout (planned)

```
sony-liv-concurrency/
├── docs/
├── clickhouse/migrations/
├── clickhouse/queries/benchmark/
├── clickhouse/scripts/
└── backend/                  # scope gated on FINAL_PLAN.md §16 Q4
```

The problem statement puts polished frontends explicitly out of scope, so the product surface is intentionally minimal and its extent is an open question ([FINAL_PLAN.md §16](docs/FINAL_PLAN.md#16-open-questions) Q4) rather than a commitment.
