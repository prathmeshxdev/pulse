export type Grain = "minute" | "hour" | "day";
export type CountUnit = "session" | "user";

export interface Dimension {
  name: string;
  source: string;
  type: string;
}

export interface Filter {
  dimension: string;
  op: "eq" | "in";
  value?: string;
  values?: string[];
}

export interface CurvePoint {
  t: string; // ISO minute/bucket
  value: number; // concurrency (minute) or peak-in-bucket (hour/day)
  avg?: number; // avg-in-bucket for hour/day
}

export interface ChartResult {
  points: CurvePoint[];
  peak: number | null;
  avg: number | null;
}

export interface Window {
  start: string | null;
  end: string | null;
}

export interface BreakdownRow {
  value: string;
  peak: number | null;
  avg: number | null;
}
