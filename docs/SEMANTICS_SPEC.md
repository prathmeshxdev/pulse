# Semantics Spec — Decision Record

**This document is the durable record of the binding semantic decisions and why they were made.** It exists so these decisions are not relitigated. Where any other doc disagrees with a decision recorded here, the other doc is a bug.

Every measurement cited comes from a read-only profile of the 905,558-row training CSV.

## 0. Relationship to the other docs

[FINAL_PLAN.md](FINAL_PLAN.md) is the **authoritative build plan**, and its §1 is the complete locked rule set (R1–R10) written to be implemented from directly. This document is its decision-record companion: fewer rules, more rationale, plus the history of what changed and why. **The two must never disagree.** The mapping:

| Decision here | Rule in [FINAL_PLAN.md](FINAL_PLAN.md) |
|---|---|
| D1 — no JSON column | §12 "Do not build" |
| D2 — pause is not active | R1 |
| D3 — buffering is active | R2 |
| D4 — average over all clock minutes | §2.3 |
| §5 — any-overlap minute attribution | R6 |
| §6 — deterministic dimension snapshot | R10 |
| §3 — UTC bucketing | §1.2 |
| §7 — open questions | §16 (**the single list**; not duplicated here) |

Two things live **only** in `FINAL_PLAN.md` and are deliberately not restated here, because duplicating them is how they drift: the open-questions list (§16) and the R9 rule that segments may be filtered by overlap with the query window but never by start time alone.

[README.md](../README.md) carries the reading order for someone arriving cold.

**Sequencing rule:** these semantics are frozen, and the hand-computed micro-fixtures in [VALIDATION.md](VALIDATION.md) Layer 2 are written, **before** any serving table is built. Everything downstream inherits them, so changing them later invalidates all prior validation work.

---

## 1. The four binding decisions

These were decided explicitly and are not open for relitigation. Each is a named constant so the position can be flipped in one place if the training answer key contradicts it.

### D1 — No `properties JSON` column. Typed columns only.

**Decision:** `session_active_segments` carries typed columns for every dimension. There is no `properties JSON` column anywhere in the schema.

**Rationale:** the CSV has 13 fixed columns and no sparse or unknown dimensions. An extensibility mechanism with its own failure modes (`max_dynamic_paths` overflow, type-hint mutations, the primary-key `ALTER` trap) cannot be justified against a schema that does not vary. The "what if dimensions increase?" requirement is already answered — better — by the narrow-delta join plus `content_dict`: a new dimension is a column on a ~52,000-row table, and neither `minute_deltas` nor its write statement changes shape.

This is a scope decision, not a performance one. The technical notes on the modern JSON type are preserved in [SCHEMA_AND_DDL.md](SCHEMA_AND_DDL.md#considered-and-rejected-properties-json-on-segments) as the answer to the 100x question, because a good answer does not have to be shipped.

### D2 — Paused playback does NOT count as active.

**Decision:** a `pause` marker closes the active segment. A `resume` marker opens a new one.

**Rationale:** the problem statement names paused playback as a failure mode in its first paragraph. The data contains explicit markers: 27,340 `pause` and 31,780 `resume` events forming **20,922 pause→resume windows** with a median of 21 seconds and p90 of 293 seconds. Counting that time would be the precise error the problem was written to punish.

**Why this needs its own mechanism:** the heartbeat grace window provably cannot exclude paused time. **42,273 heartbeats are emitted while paused**, across 10,530 of those windows, so no gap ever forms. And the median pause is 21 seconds, so even total silence would not exceed a 90-second grace. See §4.

### D3 — Buffering and stalls DO count as active.

**Decision:** `BufferStart` / `BufferEnd` and other stall signals leave the session active. They are classified as keepalive, not as leaving the active state.

**Rationale:** the viewer intends to watch and the player is foregrounded with playback intent; a stall is the platform failing the viewer, not the viewer leaving. Excluding buffering would penalise the CDN in a viewership metric.

**This is deliberately asymmetric with D2, and the asymmetry is the point.** Both are "playback is not advancing", but they differ in intent: a pause is the *viewer choosing* to stop, a buffer is the *player failing* to continue. Concurrency measures viewers watching, so viewer intent is the discriminator. It is also the larger of the two swings — 66,641 `BufferStart` and 66,289 `BufferEnd` events — so it must be stated rather than left to fall out of an implementation detail.

### D4 — Average concurrency is averaged over ALL clock minutes in the window.

**Decision:** the denominator is every clock minute in `[range_start, range_end)`, including minutes where concurrency is zero. Minutes with no delta are **gap-filled with the carried-forward concurrency value**, not skipped and not treated as zero.

**Rationale:** this is the time-weighted mean a viewer of a concurrency chart would read off the curve. The alternative — averaging only minutes that happen to contain a delta — is not a defensible metric; it is an artifact of `GROUP BY minute` emitting sparse rows.

**Two distinct bugs this closes.** First, `sum(delta) GROUP BY minute` produces a row only for minutes containing a delta, so a minute where concurrency sits flat at 50 with nobody joining or leaving contributes nothing to `avg()`. With ~52,000 segments over ~17,000 minutes, and more so after dimension filtering, long stretches of the curve have no deltas at all. Second, a range-level average must never be computed as `avg(avg_in_hour)`; an unweighted mean of hourly means equals the true range average only when every hour contributes an equal number of minute samples, which a sparse series never does.

**Implementation:** generate a dense minute grid, left-join the per-minute net deltas onto it, carry the running sum across the gaps, then take `max` and `avg` over the dense curve. Query in [SCHEMA_AND_DDL.md](SCHEMA_AND_DDL.md#benchmark-query-template-normative).

---

## 2. Event classification — the only place event semantics are defined

Playback state rides in the **`event` column inside `event_type='VideoHeartbeat'`**, not in `event_type`. There are 41 distinct `event` values under `VideoHeartbeat`, which makes `event` a state channel rather than decoration. Any state machine that dispatches on `event_type` alone cannot see pause or resume at all.

```sql
-- Normative classifier. Used by build_segments.sql, by the Python reference, and by fixtures.
multiIf(
    event_type = 'VideoSessionStart',                                    'open',
    event_type = 'VideoPlay',                                            'play',
    event_type IN ('VideoSessionEnd', 'VideoError'),                     'close',
    event_type = 'AppBackgrounded',                                      'background',
    event_type = 'AppForegrounded',                                      'foreground',
    -- Playback-state markers ride inside VideoHeartbeat via the `event` column.
    event_type = 'VideoHeartbeat'
        AND event IN ('pause', 'speed-pause', 'AdPause'),                'pause',
    event_type = 'VideoHeartbeat'
        AND event IN ('resume', 'speed-resume', 'AdResume'),             'resume',
    -- Everything else under VideoHeartbeat, including BufferStart/BufferEnd per D3.
    event_type = 'VideoHeartbeat',                                       'keepalive',
    'ignore'
) AS signal
```

### The active predicate — all four conditions required

A session contributes to concurrency at instant `t` only when all four hold:

| Condition | Set false by | Set true by |
|---|---|---|
| `started AND NOT ended` | `close` | `open` |
| `foreground` | `background` | `foreground`, `open` |
| `playing` | `pause` | `play`, `resume` |
| `heartbeat_fresh` (`t <= last_keepalive + HEARTBEAT_GRACE_SEC`) | grace elapsing | any `keepalive` or `play` |

**The `foreground` condition gates keepalive, and this is not optional.** 3,799 heartbeats are emitted while backgrounded, across 2,526 background windows. An implementation that treats `VideoHeartbeat` as unconditionally "extend active" resurrects backgrounded sessions.

### Tolerated asymmetries

`resume` while already playing is a **no-op, not an error**: there are 31,780 `resume` events against 27,340 `pause`, so unmatched resumes are common. Symmetrically, a session may end while paused, and a pause may be followed directly by `close` with no intervening `resume`.

---

## 3. Locked parameters

| Constant | Value | Confidence | Notes |
|---|---|---|---|
| `PAUSE_COUNTS_AS_ACTIVE` | **false** (D2) | Decided | 20,922 windows; percent-scale swing |
| `BUFFERING_COUNTS_AS_ACTIVE` | **true** (D3) | Decided | 66,641 windows; largest remaining swing |
| `AVERAGE_DENOMINATOR` | **all clock minutes** (D4) | Decided | Dense grid, carry-forward |
| `MINUTE_ATTRIBUTION` | **any-overlap** | Decided, low confidence in ground truth | §5; a segment counts in every minute it touches |
| `HEARTBEAT_GRACE_SEC` | **90** | Decided, near-inert | §4; moving it 60↔120 changes ~0.15% of gaps |
| `SESSION_TIMEOUT` | **none** | Decided | Only `close` ends a session; missing heartbeats end the *segment* |
| `TIMEZONE` | **UTC only** | Decided ([FINAL_PLAN.md](FINAL_PLAN.md) §1.2) | See below |

The semantic parameters belong in `clickhouse/scripts/config.env` so a flip is a one-line change, and each gets a measured peak/avg delta in the [VALIDATION.md](VALIDATION.md) sensitivity matrix.

**Timezone is locked to UTC, and the mechanism is the column type rather than convention:** every timestamp column is declared `DateTime64(3, 'UTC')` or `DateTime('UTC')`, so the `toStartOf*` functions inherit UTC from the column and cannot silently pick up a server or session timezone.

**Accepted consequence.** For an India-facing service a UTC day boundary cuts at **05:30 IST**, so a reported "peak per day" will not align with a business day as an Indian stakeholder would describe it. This is accepted because UTC is unambiguous, matches the raw epoch timestamps with no conversion step, and eliminates a whole class of off-by-5.5-hours bug from a system judged on correctness. It is also cheap to revisit: switching to IST is a **bucketing-function change confined to the query layer** (`toStartOfDay(minute, 'Asia/Kolkata')`), not a schema migration, because stored deltas are timezone-agnostic instants and only the grouping expression changes.

---

## 4. The heartbeat grace window is not the exclusion mechanism

Earlier drafts of this plan presented the 90-second heartbeat gap as the mechanism that excludes inactive time. **It is not, and it cannot be.** The measured cadence is far denser than the "~every 60 seconds" the data dictionary suggests: inter-heartbeat gaps are p50 **1 second**, p90 40 seconds, p99 80 seconds, and only **0.87% exceed 90 seconds** at all. Gaps beyond 120 seconds are 0.72%.

Two consequences.

**Inactivity is excluded by explicit markers, not by silence.** `background` and `pause` do the work. The grace window only catches genuine client death or network loss, which is 0.87% of gaps.

**Grace duration is a near-inert parameter and must stop being treated as the primary tuning knob.** The entire 60↔120 second range moves roughly 0.15% of gaps. Tuning it cannot fix a wrong answer. The parameters that move the answer at percent scale are D2, D3, D4, and minute attribution.

---

## 5. Minute attribution — any-overlap

A segment counts in **every minute it touches**:

```sql
toStartOfMinute(segment_start)                                                AS plus_minute,
toStartOfMinute(segment_end - toIntervalMillisecond(1)) + toIntervalMinute(1) AS minus_minute
```

**Why the naive convention is broken.** Emitting `+1` at `toStartOfMinute(segment_start)` and `−1` at `toStartOfMinute(segment_end)` puts both boundaries in the same row group whenever a segment starts and ends inside one minute. `SummingMergeTree` sums them to zero and the row disappears — the viewer never existed. This is not a tail case: background windows have a median of 35 seconds and pauses 21 seconds, so once background and pause splitting are implemented, **sub-minute segments are among the most common segment shapes in the dataset**. The bias is systematic and always downward.

The any-overlap expression guarantees `minus_minute > plus_minute` for every segment, so no segment can collapse. It slightly over-counts relative to a "occupied for the whole minute" reading, and there is no way to know which the ground truth uses — hence `MINUTE_ATTRIBUTION` is a config constant with a published sensitivity, and an invariant asserts no segment ever produces `plus_minute >= minus_minute`.

---

## 6. Dimension attribution — deterministic snapshot at segment start

**Dimensions are not constant within a session.** `subtitle_language` varies within **99.96%** of sessions (10,862 of 10,866), `audio_language` within 81%, `player_version` within 1,600 sessions, `platform` within 95, `user_id` within 120, `content_id` within 1. The first two rows of the CSV already show `subtitle_language` going `"unk"` → `"OFF"`.

**`any()` is not acceptable here.** ClickHouse's `any()` is explicitly non-deterministic: it returns whichever value the first-processed block carried and can differ between runs of an identical query. A benchmark filtered on `audio_language = 'hin'` could return different answers on re-run, which directly breaks the "same pipeline, reproducible evidence" requirement.

**Rule:** snapshot each dimension deterministically at segment start, scoped to the segment's own event window rather than the whole session:

```sql
argMin(platform,          (event_timestamp, event_type, event)) AS platform,
argMin(country,           (event_timestamp, event_type, event)) AS country,
argMin(audio_language,    (event_timestamp, event_type, event)) AS audio_language,
argMin(subtitle_language, (event_timestamp, event_type, event)) AS subtitle_language,
-- … etc.
```

The tuple tiebreaker makes the result total-ordered and therefore reproducible.

**Semantic choice being made:** a segment is labelled with the dimension values in force **when it started**. A viewer who switches subtitles mid-segment is attributed entirely to the original value. The fully correct alternative for filtered queries is to split the segment whenever a filterable dimension changes, which is more accurate but multiplies segment count — with `subtitle_language` varying in 99.96% of sessions it would fragment nearly every segment. Snapshotting is cheaper and defensible **because it is documented**; splitting stays available if the answer key demands it.

---

## 7. Still open

**The open questions live in one place: [FINAL_PLAN.md §16](FINAL_PLAN.md#16-open-questions).** They are not restated here, because two lists tracking overlapping sets is how a question gets answered in one document and left open in another — which had already happened before these documents were reconciled.

**Everything in §1 through §6 of this document is settled** — pause exclusion, buffering inclusion, the averaging denominator, minute attribution, UTC bucketing, and the dropped JSON column are decisions, not questions.

Two items this document previously listed as open are now **closed by R5** in the authoritative rule set, and are recorded here so the answers are not re-derived. `VideoError` ends the **segment only**, not the session, and a later `play` or `resume` legitimately opens a new one. Events arriving **after** `VideoSessionEnd` are **ignored**, because `close` is terminal — note this reverses an earlier working default of "open a new segment", and the reversal is deliberate: it costs correctness on roughly 0.1% of sessions and buys a materially simpler state machine.

One item moved rather than closed. Minute attribution is **locked** to any-overlap by R6, but it is the highest-risk locked choice in the whole design, so it appears in §16's verification table rather than in the open list. Sampling at the minute start is a plausible ground-truth generator and would *undercount* short segments where any-overlap slightly overcounts, which is why the sensitivity table reports both.

---

## 8. What changed from earlier drafts, and why

Recorded so the same ground is not re-covered.

| Earlier position | Current position | Reason |
|---|---|---|
| State machine dispatches on `event_type` | Classifies on `(event_type, event)` | `pause`/`resume` live in `event`; the old machine could not see them |
| 90s heartbeat gap excludes inactive time | Explicit `pause`/`background` markers exclude it; grace catches only client death | 42,273 heartbeats fire during pauses; 0.87% of gaps exceed 90s |
| Grace is the primary tuning knob | Grace is near-inert; D2/D3/D4 and attribution are the knobs | Measured gap distribution |
| Dedup key `(session, ts, event_type)` with `any(event)` | Key must include `event` | The old key destroyed pause/resume markers before the state machine ran |
| Dimensions constant per session, snapshot with `any()` | Vary within sessions; snapshot with `argMin` at segment start | 99.96% of sessions vary in `subtitle_language`; `any()` is non-deterministic |
| `+1` at start minute, `−1` at end minute | Any-overlap attribution | Same-minute segments collapsed to net zero and vanished |
| Deterministic `segment_id` makes rebuilds idempotent | It does not; `ReplacingMergeTree` plus semi-join does | Deterministic IDs make duplicates detectable, not absent |
| Open segment ends at `last_active + grace` | Clamped to `least(last_active + grace, watermark)` | Unclamped ends project a phantom tail past the end of known data |
| `properties JSON` on segments, adopted | Rejected (D1) | No sparse dimensions exist to justify it |
| ~10^5–10^6 segments; win on measured latency | ~10,866 sessions, ~52,000 segments, ~10^5 delta rows; win on correctness | Every team's queries will be fast at this scale |
