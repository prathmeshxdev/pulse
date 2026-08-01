import { useEffect, useState } from "react";
import type { ChartResult, Filter, Grain } from "../types";
import { getChart } from "../api";
import { Chart } from "./Chart";

interface Props {
  start: string;
  end: string;
  grain: Grain;
  filters: Filter[];
}

const fmt = (n: number | null) =>
  n === null ? "—" : n >= 1000 ? n.toLocaleString(undefined, { maximumFractionDigits: 0 }) : n.toFixed(n < 10 ? 2 : 1);

export function Dashboard({ start, end, grain, filters }: Props) {
  const [res, setRes] = useState<ChartResult | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!start || !end) return;
    setLoading(true);
    setErr(null);
    getChart(start, end, grain, filters)
      .then(setRes)
      .catch((e) => setErr(String(e)))
      .finally(() => setLoading(false));
  }, [start, end, grain, JSON.stringify(filters)]);

  const label = grain === "minute" ? "concurrency" : "peak in bucket";

  return (
    <div>
      <div className="kpis">
        <div className="kpi">
          <div className="label">Peak concurrency</div>
          <div className="value accent">{res ? fmt(res.peak) : "—"}</div>
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
        <h3>{loading ? "Loading…" : `Concurrency curve — ${label}`}</h3>
        {res && res.points.length > 0 ? (
          <Chart points={res.points} label={label} />
        ) : (
          <p className="muted">{loading ? "" : "No data for this range/filter. Set a range that overlaps the loaded data."}</p>
        )}
      </div>
    </div>
  );
}
