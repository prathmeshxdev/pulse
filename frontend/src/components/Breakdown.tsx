import { useEffect, useState } from "react";
import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { BreakdownRow, CountUnit, Filter, Grain } from "../types";
import { getBreakdown } from "../api";

interface Props {
  start: string;
  end: string;
  grain: Grain;
  unit: CountUnit;
  dimension: string;
  filters: Filter[];
}

// Breakdown renders peak concurrency per dimension value (top-N) as a horizontal
// bar chart — the "which platform/country/content peaks highest" view. Each bar
// comes from the same normative summary query filtered to that value.
export function Breakdown({ start, end, grain, unit, dimension, filters }: Props) {
  const [rows, setRows] = useState<BreakdownRow[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!start || !end || !dimension) return;
    let cancelled = false;
    setLoading(true);
    const h = setTimeout(() => {
      getBreakdown(start, end, grain, dimension, filters, unit)
        .then((r) => !cancelled && (setRows(r), setErr(null)))
        .catch((e) => !cancelled && setErr(String(e)))
        .finally(() => !cancelled && setLoading(false));
    }, 350);
    return () => {
      cancelled = true;
      clearTimeout(h);
    };
  }, [start, end, grain, unit, dimension, JSON.stringify(filters)]);

  if (!dimension) return null;

  const data = rows.map((r) => ({ value: r.value, peak: r.peak ?? 0, avg: r.avg ?? 0 }));
  const height = Math.max(160, data.length * 34 + 40);

  return (
    <div className="card" style={{ marginTop: 20 }}>
      <h3>
        Peak concurrency by {dimension}
        {loading && <span className="muted" style={{ fontWeight: 400 }}> · updating…</span>}
      </h3>
      {err && <div className="error">{err}</div>}
      {data.length === 0 && !loading ? (
        <p className="muted">No values.</p>
      ) : (
        <ResponsiveContainer width="100%" height={height}>
          <BarChart data={data} layout="vertical" margin={{ top: 4, right: 24, bottom: 4, left: 8 }}>
            <CartesianGrid stroke="var(--border)" horizontal={false} />
            <XAxis type="number" stroke="var(--muted)" fontSize={11} allowDecimals={false} />
            <YAxis type="category" dataKey="value" stroke="var(--muted)" fontSize={11} width={130} />
            <Tooltip
              contentStyle={{ background: "var(--panel-2)", border: "1px solid var(--border)", borderRadius: 8, color: "var(--text)" }}
              formatter={(v: number, name: string) => [Math.round(v), name === "peak" ? "peak" : "avg"]}
              cursor={{ fill: "rgba(255,255,255,0.04)" }}
            />
            <Bar dataKey="peak" radius={[0, 4, 4, 0]}>
              {data.map((_, i) => (
                <Cell key={i} fill={i === 0 ? "var(--accent)" : "var(--accent-2)"} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      )}
      <p className="muted" style={{ marginTop: 8 }}>
        Top {data.length} by session volume, ranked by peak. Each bar is the normative summary query filtered to that
        value — so it matches the single-filter view exactly.
      </p>
    </div>
  );
}
