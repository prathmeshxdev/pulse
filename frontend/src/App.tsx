import { useEffect, useState } from "react";
import type { Dimension, Filter, Grain } from "./types";
import { getDimensions, getWindow } from "./api";
import { FilterSidebar } from "./components/FilterSidebar";
import { Dashboard } from "./components/Dashboard";
import { ReplayView } from "./components/ReplayView";

const toInput = (iso: string | null): string => (iso ? iso.replace("Z", "").slice(0, 16) : "");

export default function App() {
  const [tab, setTab] = useState<"dashboard" | "replay">("dashboard");
  const [dimensions, setDimensions] = useState<Dimension[]>([]);
  const [filters, setFilters] = useState<Filter[]>([]);
  const [start, setStart] = useState("");
  const [end, setEnd] = useState("");
  const [grain, setGrain] = useState<Grain>("minute");
  const [breakdownDim, setBreakdownDim] = useState("");
  const [bootError, setBootError] = useState<string | null>(null);

  useEffect(() => {
    getDimensions().then(setDimensions).catch((e) => setBootError(String(e)));
    getWindow()
      .then((w) => {
        if (w.start) setStart(toInput(w.start));
        if (w.end) setEnd(toInput(w.end));
      })
      .catch((e) => setBootError(String(e)));
  }, []);

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="brand">
          <span className="dot" />
          <h1>Pulse</h1>
        </div>
        <p className="tagline">Foreground-only concurrency · minute / hour / day</p>

        <div className="field" style={{ marginBottom: 14 }}>
          <label>Grain</label>
          <div className="seg">
            {(["minute", "hour", "day"] as Grain[]).map((g) => (
              <button key={g} className={grain === g ? "active" : ""} onClick={() => setGrain(g)}>
                {g}
              </button>
            ))}
          </div>
        </div>

        <div className="field">
          <label>Start (UTC)</label>
          <input type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} />
        </div>
        <div className="field" style={{ marginTop: 12 }}>
          <label>End (UTC)</label>
          <input type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} />
        </div>

        <hr />
        <FilterSidebar dimensions={dimensions} filters={filters} onChange={setFilters} />

        <hr />
        <div className="field">
          <label>Break down by</label>
          <select value={breakdownDim} onChange={(e) => setBreakdownDim(e.target.value)}>
            <option value="">none</option>
            {dimensions.map((d) => (
              <option key={d.name} value={d.name}>
                {d.name}
              </option>
            ))}
          </select>
        </div>
      </aside>

      <main className="main">
        <div className="tabs">
          <button className={tab === "dashboard" ? "active" : ""} onClick={() => setTab("dashboard")}>
            Dashboard
          </button>
          <button className={tab === "replay" ? "active" : ""} onClick={() => setTab("replay")}>
            Live replay
          </button>
        </div>

        {bootError && <div className="error">Backend not reachable: {bootError}</div>}

        {tab === "dashboard" ? (
          <Dashboard start={start} end={end} grain={grain} filters={filters} breakdownDim={breakdownDim} />
        ) : (
          <ReplayView start={start} end={end} grain={grain} filters={filters} />
        )}
      </main>
    </div>
  );
}
