import { useEffect, useMemo, useRef, useState } from "react";
import type { ChartResult, CountUnit, Filter, Grain } from "../types";
import { getChart } from "../api";
import { Chart } from "./Chart";
import { Breakdown } from "./Breakdown";
import { downsample, fmtTime, peakPoint } from "../util";

const MAX_POINTS = 3000;

const fmt = (n: number | null) =>
  n === null ? "—" : n >= 1000 ? Math.round(n).toLocaleString() : n.toFixed(n < 10 ? 2 : 1);

const unitLabel = (unit: CountUnit) => (unit === "session" ? "session-aware" : "unique users");

interface Props {
  start: string;
  end: string;
  grain: Grain;
  unit: CountUnit;
  filters: Filter[];
  breakdownDim: string;
}

// ReplayView fetches the curve once at the selected grain/unit/filters, then reveals
// it point-by-point to recreate the "concurrency builds in near real time" demo.
export function ReplayView({ start, end, grain, unit, filters, breakdownDim }: Props) {
  const [full, setFull] = useState<ChartResult | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [i, setI] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState(20);
  const timer = useRef<number | null>(null);

  useEffect(() => {
    if (!start || !end) return;
    let cancelled = false;
    setLoading(true);
    setPlaying(false);
    setI(0);
    const h = setTimeout(() => {
      getChart(start, end, grain, filters, unit)
        .then((r) => !cancelled && (setFull(r), setErr(null), setI(0)))
        .catch((e) => !cancelled && setErr(String(e)))
        .finally(() => !cancelled && setLoading(false));
    }, 350);
    return () => {
      cancelled = true;
      clearTimeout(h);
    };
  }, [start, end, grain, unit, JSON.stringify(filters)]);

  const points = useMemo(() => (full ? downsample(full.points, MAX_POINTS) : []), [full]);
  const peakAt = useMemo(() => (full ? peakPoint(full.points) : null), [full]);

  useEffect(() => {
    if (!playing || points.length === 0) return;
    const step = Math.max(1, Math.round(speed / 20));
    const tickMs = 1000 / Math.min(speed, 20);
    timer.current = window.setInterval(() => {
      setI((prev) => {
        if (prev >= points.length) {
          setPlaying(false);
          return prev;
        }
        return prev + step;
      });
    }, tickMs);
    return () => {
      if (timer.current) window.clearInterval(timer.current);
    };
  }, [playing, speed, points]);

  const shown = useMemo(() => points.slice(0, Math.max(1, Math.min(i, points.length))), [points, i]);
  const curVal = shown.length ? shown[shown.length - 1].value : 0;
  const curT = shown.length ? shown[shown.length - 1].t : "";
  const atEnd = i >= points.length;
  const label = grain === "minute" ? "concurrency" : "peak in bucket";

  return (
    <div>
      {err && <div className="error">{err}</div>}

      <div className="agg-panels">
        <div className="agg-panel">
          <div className="agg-head">
            <span className="agg-title">Peak</span>
            {loading && <span className="muted">updating…</span>}
          </div>
          <div className="agg-value accent">{full ? fmt(full.peak) : "—"}</div>
          {peakAt && <div className="agg-sub muted">at {fmtTime(peakAt.t, grain)} UTC</div>}
          <p className="agg-desc muted">Final max ({unitLabel(unit)}).</p>
        </div>

        <div className="agg-panel">
          <div className="agg-head">
            <span className="agg-title">Average</span>
            {loading && <span className="muted">updating…</span>}
          </div>
          <div className="agg-value">{full ? fmt(full.avg) : "—"}</div>
          <p className="agg-desc muted">Final mean over window ({unitLabel(unit)}).</p>
        </div>

        <div className="agg-panel agg-panel-wide">
          <div className="agg-head">
            <span className="agg-title">Timeseries replay</span>
            <span className="muted">
              {grain} · {label} · {unitLabel(unit)}
              {!loading && points.length > 0 && (
                <> · now {Math.round(curVal).toLocaleString()} at {curT ? fmtTime(curT, grain) : "—"} UTC</>
              )}
            </span>
          </div>

          {loading ? (
            <p className="muted">Loading curve…</p>
          ) : points.length > 0 ? (
            <>
              <div className="replay-bar">
                <button
                  className="primary"
                  onClick={() => (atEnd ? (setI(0), setPlaying(true)) : setPlaying(!playing))}
                >
                  {playing ? "❚❚ Pause" : atEnd ? "↻ Restart" : "▶ Play"}
                </button>
                <input
                  type="range"
                  min={0}
                  max={points.length}
                  value={Math.min(i, points.length)}
                  onChange={(e) => {
                    setPlaying(false);
                    setI(Number(e.target.value));
                  }}
                />
                <label className="muted">
                  speed&nbsp;
                  <select value={speed} onChange={(e) => setSpeed(Number(e.target.value))}>
                    <option value={10}>10×</option>
                    <option value={20}>20×</option>
                    <option value={60}>60×</option>
                    <option value={200}>200×</option>
                  </select>
                </label>
                <span className="muted">
                  {Math.min(i, points.length).toLocaleString()}/{points.length.toLocaleString()} buckets
                </span>
              </div>
              <Chart points={shown} label={label} grain={grain} />
            </>
          ) : (
            <p className="muted">No data for this range/filter.</p>
          )}

          {full && full.points.length > MAX_POINTS && (
            <p className="muted agg-foot">
              Animating {MAX_POINTS.toLocaleString()} of {full.points.length.toLocaleString()} points;
              peak/avg are exact from the serving query.
            </p>
          )}
        </div>
      </div>

      {breakdownDim && (
        <Breakdown start={start} end={end} grain={grain} unit={unit} dimension={breakdownDim} filters={filters} />
      )}
    </div>
  );
}
