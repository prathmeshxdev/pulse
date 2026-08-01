import { useEffect, useMemo, useState } from "react";
import type { ChartResult, Filter, Grain } from "../types";
import { getChart } from "../api";
import { Chart } from "./Chart";
import { Breakdown } from "./Breakdown";
import { downsample, fmtTime, peakPoint } from "../util";

interface Props {
  start: string;
  end: string;
  grain: Grain;
  filters: Filter[];
  breakdownDim: string;
}

const MAX_POINTS = 2000;

const fmt = (n: number | null) =>
  n === null ? "—" : n >= 1000 ? Math.round(n).toLocaleString() : n.toFixed(n < 10 ? 2 : 1);

export function Dashboard({ start, end, grain, filters, breakdownDim }: Props) {
  const [res, setRes] = useState<ChartResult | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  // Debounced fetch: typing a date / toggling filters shouldn't fire a query
  // per keystroke. Keep the previous chart visible while the new one loads.
  useEffect(() => {
    if (!start || !end) return;
    let cancelled = false;
    setLoading(true);
    const h = setTimeout(() => {
      getChart(start, end, grain, filters)
        .then((r) => !cancelled && (setRes(r), setErr(null)))
        .catch((e) => !cancelled && setErr(String(e)))
        .finally(() => !cancelled && setLoading(false));
    }, 350);
    return () => {
      cancelled = true;
      clearTimeout(h);
    };
  }, [start, end, grain, JSON.stringify(filters)]);

  const label = grain === "minute" ? "concurrency" : "peak in bucket";
  const shown = useMemo(() => (res ? downsample(res.points, MAX_POINTS) : []), [res]);
  const peakAt = useMemo(() => (res ? peakPoint(res.points) : null), [res]);

  return (
    <div>
      <div className="kpis">
        <div className="kpi">
          <div className="label">Peak concurrency</div>
          <div className="value accent">{res ? fmt(res.peak) : "—"}</div>
          {peakAt && <div className="muted" style={{ marginTop: 4 }}>at {fmtTime(peakAt.t, grain)} UTC</div>}
        </div>
        <div className="kpi">
          <div className="label">Avg concurrency</div>
          <div className="value">{res ? fmt(res.avg) : "—"}</div>
        </div>
        <div className="kpi">
          <div className="label">Buckets ({grain})</div>
          <div className="value">{res ? res.points.length.toLocaleString() : "—"}</div>
        </div>
        <div className="kpi">
          <div className="label">Filters</div>
          <div className="value">{filters.length}</div>
        </div>
      </div>

      {err && <div className="error">{err}</div>}

      <div className="card">
        <h3>
          Concurrency curve — {label}
          {loading && <span className="muted" style={{ fontWeight: 400 }}> · updating…</span>}
        </h3>
        {shown.length > 0 ? (
          <Chart points={shown} label={label} grain={grain} />
        ) : (
          <p className="muted">{loading ? "" : "No data for this range/filter."}</p>
        )}
        {res && res.points.length > MAX_POINTS && (
          <p className="muted" style={{ marginTop: 8 }}>
            Showing {MAX_POINTS.toLocaleString()} of {res.points.length.toLocaleString()} points (peak-preserving
            downsample); peak/avg KPIs are exact from the serving query.
          </p>
        )}
      </div>

      {breakdownDim && (
        <Breakdown start={start} end={end} grain={grain} dimension={breakdownDim} filters={filters} />
      )}
    </div>
  );
}
