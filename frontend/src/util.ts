import type { CurvePoint, Grain } from "./types";

// downsample keeps at most `max` points by bucketing and taking the MAX of each
// bucket — preserving peaks (what matters for a concurrency curve) rather than
// blurring them like stride sampling would. A 17k-minute curve renders smoothly
// at ~2k points with the peak intact.
export function downsample(points: CurvePoint[], max: number): CurvePoint[] {
  if (points.length <= max) return points;
  const bucket = Math.ceil(points.length / max);
  const out: CurvePoint[] = [];
  for (let i = 0; i < points.length; i += bucket) {
    let mp = points[i];
    for (let j = i + 1; j < Math.min(i + bucket, points.length); j++) {
      if (points[j].value > mp.value) mp = points[j];
    }
    out.push(mp);
  }
  return out;
}

// peakPoint returns the point with the maximum value (the "peak at" readout).
export function peakPoint(points: CurvePoint[]): CurvePoint | null {
  if (points.length === 0) return null;
  let mp = points[0];
  for (const p of points) if (p.value > mp.value) mp = p;
  return mp;
}

const pad = (n: number) => String(n).padStart(2, "0");

// Format a bucket timestamp appropriately for the grain.
export function fmtTime(iso: string, grain: Grain): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const mm = pad(d.getUTCMonth() + 1), dd = pad(d.getUTCDate());
  const hh = pad(d.getUTCHours()), mi = pad(d.getUTCMinutes());
  if (grain === "day") return `${mm}-${dd}`;
  if (grain === "hour") return `${mm}-${dd} ${hh}:00`;
  return `${mm}-${dd} ${hh}:${mi}`;
}
