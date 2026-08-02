import { useEffect, useState } from "react";
import type { Dimension, Filter } from "../types";
import { getValues } from "../api";

interface Props {
  dimensions: Dimension[];
  filters: Filter[];
  onChange: (f: Filter[]) => void;
}

// One draft filter builder: pick dimension → load its distinct values → pick value → add.
export function FilterSidebar({ dimensions, filters, onChange }: Props) {
  const [dim, setDim] = useState("");
  const [values, setValues] = useState<string[]>([]);
  const [value, setValue] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!dim) {
      setValues([]);
      return;
    }
    setLoading(true);
    getValues(dim)
      .then(setValues)
      .catch(() => setValues([]))
      .finally(() => setLoading(false));
  }, [dim]);

  const add = () => {
    if (!dim || !value) return;
    if (filters.some((f) => f.dimension === dim && f.value === value)) return;
    onChange([...filters, { dimension: dim, op: "eq", value }]);
    setValue("");
  };

  const remove = (i: number) => onChange(filters.filter((_, idx) => idx !== i));

  return (
    <div>
      <label style={{ fontSize: 11, textTransform: "uppercase", color: "var(--muted)", letterSpacing: 0.5 }}>
        Filters
      </label>
      <div className="filter-row" style={{ marginTop: 8 }}>
        <select value={dim} onChange={(e) => setDim(e.target.value)}>
          <option value="">dimension…</option>
          {dimensions.map((d) => (
            <option key={d.name} value={d.name}>
              {d.name}
            </option>
          ))}
        </select>
      </div>
      <div className="filter-row">
        {values.length > 0 ? (
          <select value={value} onChange={(e) => setValue(e.target.value)}>
            <option value="">{loading ? "loading…" : "value…"}</option>
            {values.map((v) => (
              <option key={v} value={v}>
                {v}
              </option>
            ))}
          </select>
        ) : (
          <input placeholder={loading ? "loading…" : "value"} value={value} onChange={(e) => setValue(e.target.value)} />
        )}
        <button className="primary" onClick={add}>
          +
        </button>
      </div>

      <div style={{ marginTop: 10 }}>
        {filters.length === 0 && <span className="muted">No filters — full dataset.</span>}
        {filters.map((f, i) => (
          <span className="chip" key={`${f.dimension}-${f.value}-${i}`}>
            {f.dimension} = {f.value}
            <span className="x" onClick={() => remove(i)}>
              ✕
            </span>
          </span>
        ))}
      </div>
    </div>
  );
}
