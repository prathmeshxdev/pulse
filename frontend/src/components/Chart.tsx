import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { CurvePoint, Grain } from "../types";
import { fmtTime } from "../util";

export function Chart({ points, label, grain }: { points: CurvePoint[]; label: string; grain: Grain }) {
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
        <XAxis dataKey="t" tickFormatter={(t) => fmtTime(t, grain)} stroke="var(--muted)" fontSize={11} minTickGap={48} />
        <YAxis stroke="var(--muted)" fontSize={11} width={48} allowDecimals={false} />
        <Tooltip
          contentStyle={{ background: "var(--panel-2)", border: "1px solid var(--border)", borderRadius: 8, color: "var(--text)" }}
          labelFormatter={(t) => fmtTime(String(t), grain)}
          formatter={(v: number) => [Math.round(v), label]}
        />
        <Area
          type="monotone"
          dataKey="value"
          stroke="var(--accent)"
          strokeWidth={2}
          fill="url(#g)"
          dot={false}
          isAnimationActive={points.length <= 400}
          animationDuration={300}
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}
