import { useEffect, useMemo, useRef, useState } from "react";
import type { ChartResult, Filter } from "../types";
import { getChart } from "../api";
import { Chart } from "./Chart";

// ReplayView fetches the full minute curve once, then reveals it minute-by-minute
// to recreate the "concurrency builds in near real time" demo. Pure client-side
// animation over the served curve — no extra backend path.
export function ReplayView({ start, end, filters }: { start: string; end: string; filters: Filter[] }) {
  const [full, setFull] = useState<ChartResult | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [i, setI] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState(20); // minutes revealed per second
  const timer = useRef<number | null>(null);

  useEffect(() => {
    if (!start || !end) return;
    setErr(null);
    setPlaying(false);
    setI(0);
    getChart(start, end, "minute", filters)
      .then((r) => {
        setFull(r);
        setI(0);
      })
      .catch((e) => setErr(String(e)));
  }, [start, end, JSON.stringify(filters)]);

  useEffect(() => {
    if (!playing || !full) return;
    timer.current = window.setInterval(() => {
      setI((prev) => {
        if (prev >= full.points.length) {
          setPlaying(false);
          return prev;
        }
        return prev + 1;
      });
    }, 1000 / speed);
    return () => {
      if (timer.current) window.clearInterval(timer.current);
    };
  }, [playing, speed, full]);

  const shown = useMemo(() => (full ? full.points.slice(0, Math.max(1, i)) : []), [full, i]);
  const curVal = shown.length ? shown[shown.length - 1].value : 0;
  const runningPeak = useMemo(() => shown.reduce((m, p) => Math.max(m, p.value), 0), [shown]);

  if (err) return <div className="error">{err}</div>;
  if (!full) return <p className="muted">Loading curve…</p>;

  const atEnd = i >= full.points.length;

  return (
    <div>
      <div className="kpis">
        <div className="kpi">
          <div className="label">Now watching</div>
          <div className="value accent">{Math.round(curVal).toLocaleString()}</div>
        </div>
        <div className="kpi">
          <div className="label">Running peak</div>
          <div className="value">{Math.round(runningPeak).toLocaleString()}</div>
        </div>
        <div className="kpi">
          <div className="label">Minute</div>
          <div className="value">
            {Math.min(i, full.points.length)}/{full.points.length}
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
            max={full.points.length}
            value={i}
            onChange={(e) => {
              setPlaying(false);
              setI(Number(e.target.value));
            }}
          />
          <label className="muted">
            speed&nbsp;
            <select value={speed} onChange={(e) => setSpeed(Number(e.target.value))}>
              <option value={5}>5×</option>
              <option value={20}>20×</option>
              <option value={60}>60×</option>
              <option value={200}>200×</option>
            </select>
          </label>
        </div>
        <Chart points={shown} label="concurrency" />
      </div>
    </div>
  );
}
