import type { BreakdownRow, ChartResult, CountUnit, CurvePoint, Dimension, Filter, Grain, Window } from "./types";

const num = (v: unknown): number | null =>
  v === null || v === undefined ? null : typeof v === "number" ? v : Number(v);

// ClickHouse via the API returns "2026-08-01T13:00:00Z" or similar; the
// backend accepts "YYYY-MM-DDTHH:MM:SS". datetime-local omits seconds.
function normalizeDT(v: string): string {
  if (!v) return v;
  if (v.length === 16) return v + ":00"; // add seconds
  return v;
}

async function getJSON<T>(url: string): Promise<T> {
  const r = await fetch(url);
  if (!r.ok) throw new Error(`${url}: ${r.status} ${await r.text()}`);
  return r.json() as Promise<T>;
}

export async function getDimensions(): Promise<Dimension[]> {
  const d = await getJSON<{ dimensions: Dimension[] }>("/api/v1/schema/dimensions");
  return d.dimensions.sort((a, b) => a.name.localeCompare(b.name));
}

export async function getValues(dimension: string): Promise<string[]> {
  const d = await getJSON<{ values: unknown[] }>(
    `/api/v1/schema/values?dimension=${encodeURIComponent(dimension)}`
  );
  return d.values.map(String);
}

export async function getWindow(): Promise<Window> {
  return getJSON<Window>("/api/v1/schema/window");
}

interface ChartBody {
  start: string;
  end: string;
  grain: Grain;
  metric: "summary" | "timeseries";
  unit: CountUnit;
  filters: Filter[];
}

async function postChart(body: ChartBody): Promise<{ rows: Record<string, unknown>[]; peak: unknown; avg: unknown }> {
  const r = await fetch("/api/v1/concurrency/chart", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...body, start: normalizeDT(body.start), end: normalizeDT(body.end) }),
  });
  if (!r.ok) throw new Error(`chart: ${r.status} ${await r.text()}`);
  return r.json();
}

// getChart runs one summary call (peak/avg over the range) and one timeseries
// call (the curve), then normalizes into a single ChartResult.
export async function getChart(
  start: string,
  end: string,
  grain: Grain,
  filters: Filter[],
  unit: CountUnit = "session"
): Promise<ChartResult> {
  const [summary, series] = await Promise.all([
    postChart({ start, end, grain, metric: "summary", unit, filters }),
    postChart({ start, end, grain, metric: "timeseries", unit, filters }),
  ]);

  const points: CurvePoint[] = series.rows.map((row) => {
    if (grain === "minute") {
      return { t: String(row.minute), value: num(row.concurrency) ?? 0 };
    }
    return { t: String(row.bucket), value: num(row.peak) ?? 0, avg: num(row.avg) ?? undefined };
  });

  return {
    points,
    peak: num(summary.peak) ?? (summary.rows[0] ? num(summary.rows[0].peak_concurrency) : null),
    avg: num(summary.avg) ?? (summary.rows[0] ? num(summary.rows[0].avg_concurrency) : null),
  };
}

// getBreakdown returns peak+avg concurrency per value of a dimension (top-N).
export async function getBreakdown(
  start: string,
  end: string,
  grain: Grain,
  dimension: string,
  filters: Filter[],
  unit: CountUnit = "session"
): Promise<BreakdownRow[]> {
  const r = await fetch("/api/v1/concurrency/breakdown", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ start: normalizeDT(start), end: normalizeDT(end), grain, dimension, unit, filters }),
  });
  if (!r.ok) throw new Error(`breakdown: ${r.status} ${await r.text()}`);
  const d = (await r.json()) as { rows: BreakdownRow[] };
  return d.rows ?? [];
}
