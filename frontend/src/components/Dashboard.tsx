import { useEffect, useMemo, useState } from "react";
import type { ChartResult, CountUnit, Filter, Grain } from "../types";
import { getChart } from "../api";
import { Chart } from "./Chart";
import { Breakdown } from "./Breakdown";
import { downsample, fmtTime, peakPoint } from "../util";

interface Props {
  start: string;
  end: string;
  grain: Grain;
  unit: CountUnit;
  filters: Filter[];
  breakdownDim: string;
}

const MAX_POINTS = 2000;

const fmt = (n: number | null) =>
  n === null ? "—" : n >= 1000 ? Math.round(n).toLocaleString() : n.toFixed(n < 10 ? 2 : 1);

const unitLabel = (unit: CountUnit) => (unit === "session" ? "session-aware" : "unique users");

export function Dashboard({ start, end, grain, unit, filters, breakdownDim }: Props) {
  const [res, setRes] = useState<ChartResult | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!start || !end) return;
    let cancelled = false;
    setLoading(true);
    const h = setTimeout(() => {
      getChart(start, end, grain, filters, unit)
        .then((r) => !cancelled && (setRes(r), setErr(null)))
        .catch((e) => !cancelled && setErr(String(e)))
        .finally(() => !cancelled && setLoading(false));
    }, 350);
    return () => {
      cancelled = true;
      clearTimeout(h);
    };
  }, [start, end, grain, unit, JSON.stringify(filters)]);

  const label = grain === "minute" ? "concurrency" : "peak in bucket";
  const shown = useMemo(() => (res ? downsample(res.points, MAX_POINTS) : []), [res]);
  const peakAt = useMemo(() => (res ? peakPoint(res.points) : null), [res]);

  return (
    <div>
      {err && <div className="error">{err}</div>}

      <div className="agg-panels">
        <div className="agg-panel">
          <div className="agg-head">
            <span className="agg-title">Peak</span>
            {loading && <span className="muted">updating…</span>}
          </div>
          <div className="agg-value accent">{res ? fmt(res.peak) : "—"}</div>
          {peakAt && (
            <div className="agg-sub muted">at {fmtTime(peakAt.t, grain)} UTC</div>
          )}
          <p className="agg-desc muted">Max foreground-active concurrency ({unitLabel(unit)}).</p>
        </div>

        <div className="agg-panel">
          <div className="agg-head">
            <span className="agg-title">Average</span>
            {loading && <span className="muted">updating…</span>}
          </div>
          <div className="agg-value">{res ? fmt(res.avg) : "—"}</div>
          <p className="agg-desc muted">Mean over all clock minutes ({unitLabel(unit)}).</p>
        </div>

        <div className="agg-panel agg-panel-wide">
          <div className="agg-head">
            <span className="agg-title">Timeseries</span>
            <span className="muted">{grain} grain · {label} · {unitLabel(unit)}</span>
          </div>
          {shown.length > 0 ? (
            <Chart points={shown} label={label} grain={grain} />
          ) : (
            <p className="muted">{loading ? "" : "No data for this range/filter."}</p>
          )}
          {res && res.points.length > MAX_POINTS && (
            <p className="muted agg-foot">
              Showing {MAX_POINTS.toLocaleString()} of {res.points.length.toLocaleString()} points;
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
