import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { CurvePoint } from "../types";

const fmtT = (t: string) => {
  const d = new Date(t);
  if (isNaN(d.getTime())) return t;
  return d.toISOString().slice(5, 16).replace("T", " ");
};

export function Chart({ points, label }: { points: CurvePoint[]; label: string }) {
  return (
    <ResponsiveContainer width="100%" height={360}>
      <AreaChart data={points} margin={{ top: 8, right: 16, bottom: 4, left: 0 }}>
        <defs>
          <linearGradient id="g" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--accent)" stopOpacity={0.35} />
            <stop offset="100%" stopColor="var(--accent)" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid stroke="var(--border)" vertical={false} />
        <XAxis dataKey="t" tickFormatter={fmtT} stroke="var(--muted)" fontSize={11} minTickGap={40} />
        <YAxis stroke="var(--muted)" fontSize={11} width={48} allowDecimals={false} />
        <Tooltip
          contentStyle={{ background: "var(--panel-2)", border: "1px solid var(--border)", borderRadius: 8, color: "var(--text)" }}
          labelFormatter={fmtT}
          formatter={(v: number) => [v, label]}
        />
        <Area type="monotone" dataKey="value" stroke="var(--accent)" strokeWidth={2} fill="url(#g)" isAnimationActive={false} />
      </AreaChart>
    </ResponsiveContainer>
  );
}
