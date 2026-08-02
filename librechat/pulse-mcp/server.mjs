#!/usr/bin/env node
/**
 * Pulse API MCP — SSE proxy to the Go chart/breakdown compiler.
 * LibreChat agents call these tools instead of hand-written ClickHouse SQL,
 * so answers match the dashboard exactly.
 */
import express from "express";
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { SSEServerTransport } from "@modelcontextprotocol/sdk/server/sse.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";

const PORT = Number(process.env.PULSE_MCP_PORT || 8002);
const PULSE_API = (process.env.PULSE_API_URL || "http://localhost:8080").replace(/\/$/, "");

async function pulsePost(path, body) {
  const r = await fetch(`${PULSE_API}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const text = await r.text();
  if (!r.ok) throw new Error(`${path}: ${r.status} ${text}`);
  return text;
}

async function pulseGet(path) {
  const r = await fetch(`${PULSE_API}${path}`);
  const text = await r.text();
  if (!r.ok) throw new Error(`${path}: ${r.status} ${text}`);
  return text;
}

function makeServer() {
  const server = new Server(
    { name: "pulse-api", version: "1.0.0" },
    { capabilities: { tools: {} } }
  );

  server.setRequestHandler(ListToolsRequestSchema, async () => ({
    tools: [
      {
        name: "concurrency_chart",
        description:
          "Peak, average, or timeseries concurrency via the Pulse serving compiler (same as the dashboard). " +
          "Use metric=summary for peak+avg, metric=timeseries for the curve.",
        inputSchema: {
          type: "object",
          properties: {
            start: { type: "string", description: "Window start UTC, e.g. 2026-07-15T13:00:00" },
            end: { type: "string", description: "Window end UTC (exclusive-style end minute)" },
            grain: { type: "string", enum: ["minute", "hour", "day"], default: "minute" },
            metric: { type: "string", enum: ["summary", "timeseries", "peak", "avg"], default: "summary" },
            unit: { type: "string", enum: ["session", "user"], description: "session=default; user=session-independent" },
            filters: {
              type: "array",
              description: "Dimension filters, e.g. [{dimension:platform,op:eq,value:ANDROID_PHONE}]",
              items: {
                type: "object",
                properties: {
                  dimension: { type: "string" },
                  op: { type: "string", enum: ["eq", "in"] },
                  value: { type: "string" },
                  values: { type: "array", items: { type: "string" } },
                },
                required: ["dimension", "op"],
              },
            },
          },
          required: ["start", "end"],
        },
      },
      {
        name: "concurrency_breakdown",
        description: "Peak+avg per dimension value (top-N). Same compiler as the dashboard breakdown table.",
        inputSchema: {
          type: "object",
          properties: {
            start: { type: "string" },
            end: { type: "string" },
            grain: { type: "string", enum: ["minute", "hour", "day"], default: "minute" },
            dimension: { type: "string", description: "e.g. platform, country, video_type" },
            limit: { type: "integer", minimum: 1, maximum: 20, default: 10 },
            filters: { type: "array", items: { type: "object" } },
          },
          required: ["start", "end", "dimension"],
        },
      },
      {
        name: "schema_window",
        description: "Data time bounds available in the serving layer (UTC).",
        inputSchema: { type: "object", properties: {} },
      },
      {
        name: "schema_dimensions",
        description: "Filterable dimensions (static + dynamic properties.*).",
        inputSchema: { type: "object", properties: {} },
      },
    ],
  }));

  server.setRequestHandler(CallToolRequestSchema, async (req) => {
    try {
      const { name, arguments: args = {} } = req.params;
      let text;
      switch (name) {
        case "concurrency_chart":
          text = await pulsePost("/api/v1/concurrency/chart", args);
          break;
        case "concurrency_breakdown":
          text = await pulsePost("/api/v1/concurrency/breakdown", args);
          break;
        case "schema_window":
          text = await pulseGet("/api/v1/schema/window");
          break;
        case "schema_dimensions":
          text = await pulseGet("/api/v1/schema/dimensions");
          break;
        default:
          throw new Error(`unknown tool: ${name}`);
      }
      return { content: [{ type: "text", text }] };
    } catch (e) {
      return {
        content: [{ type: "text", text: `Error: ${e.message}` }],
        isError: true,
      };
    }
  });

  return server;
}

const app = express();
const transports = new Map();

app.get("/sse", async (_req, res) => {
  const transport = new SSEServerTransport("/message", res);
  transports.set(transport.sessionId, transport);
  res.on("close", () => transports.delete(transport.sessionId));
  const server = makeServer();
  await server.connect(transport);
});

app.post("/message", express.json(), async (req, res) => {
  const sessionId = req.query.sessionId;
  const transport = transports.get(sessionId);
  if (!transport) {
    res.status(404).json({ error: "unknown session" });
    return;
  }
  await transport.handlePostMessage(req, res, req.body);
});

app.get("/health", (_req, res) => res.json({ status: "ok", pulse_api: PULSE_API }));

app.listen(PORT, "0.0.0.0", () => {
  console.log(`pulse-mcp SSE on :${PORT} → ${PULSE_API}`);
});
