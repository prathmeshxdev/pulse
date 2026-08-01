import { useEffect, useMemo, useRef, useState } from "react";
import type { ChartResult, Filter, Grain } from "../types";
import { getChart } from "../api";
import { Chart } from "./Chart";
import { downsample, fmtTime } from "../util";

const MAX_POINTS = 3000;

// ReplayView fetches the curve once at the SELECTED grain, then reveals it
// point-by-point to recreate the "concurrency builds in near real time" demo.
export function ReplayView({ start, end, grain, filters }: { start: string; end: string; grain: Grain; filters: Filter[] }) {
  const [full, setFull] = useState<ChartResult | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [i, setI] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState(20); // points revealed per second
  const timer = useRef<number | null>(null);

  // Downsampled once; replay animates over these points (respects grain).
  const points = useMemo(() => (full ? downsample(full.points, MAX_POINTS) : []), [full]);

  useEffect(() => {
    if (!start || !end) return;
    setErr(null);
    setPlaying(false);
    setI(0);
    getChart(start, end, grain, filters)
      .then((r) => (setFull(r), setI(0)))
      .catch((e) => setErr(String(e)));
  }, [start, end, grain, JSON.stringify(filters)]);

  useEffect(() => {
    if (!playing || points.length === 0) return;
    // Step in chunks so long series still finish quickly and render smoothly.
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
  const runningPeak = useMemo(() => shown.reduce((m, p) => Math.max(m, p.value), 0), [shown]);

  if (err) return <div className="error">{err}</div>;
  if (!full) return <p className="muted">Loading curve…</p>;

  const label = grain === "minute" ? "concurrency" : "peak in bucket";
  const atEnd = i >= points.length;

  return (
    <div>
      <div className="kpis">
        <div className="kpi">
          <div className="label">Now watching</div>
          <div className="value accent">{Math.round(curVal).toLocaleString()}</div>
          {curT && <div className="muted" style={{ marginTop: 4 }}>{fmtTime(curT, grain)} UTC</div>}
        </div>
        <div className="kpi">
          <div className="label">Running peak</div>
          <div className="value">{Math.round(runningPeak).toLocaleString()}</div>
        </div>
        <div className="kpi">
          <div className="label">{grain} buckets</div>
          <div className="value">
            {Math.min(i, points.length).toLocaleString()}/{points.length.toLocaleString()}
          </div>
        </div>
      </div>

      <div className="card">
        <div className="replay-bar">
          <button className="primary" onClick={() => (atEnd ? (setI(0), setPlaying(true)) : setPlaying(!playing))}>
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
        </div>
        <Chart points={shown} label={label} grain={grain} />
      </div>
    </div>
  );
}
