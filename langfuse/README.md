# Langfuse — LLM observability for Pulse

Traces **LibreChat → LiteLLM → model** (prompts, tokens, MCP tool calls).
Complements **ClickStack** (Pulse API / pipeline → ClickHouse Cloud `otel_*`).

## System context

Langfuse sits in the **observability layer** (top) of the four-layer Pulse HLD.
ClickStack covers API/pipeline spans; Langfuse covers conversational LLM traces.

![Pulse system HLD](../presentations/pulse-by-layers/public/hld.png)

Solid lines: implemented paths. Dotted lines: direct MCP-to-ClickHouse reads or
future components (metadata registry, Kafka ingestion).

| Resource | Purpose |
|----------|---------|
| [`presentations/pulse-by-layers/`](../presentations/pulse-by-layers/) | Full architecture deck (Slidev) |
| [`librechat/system_prompt.md`](../librechat/system_prompt.md) | Serving tables + semantics for MCP |
| [`clickhouse/scripts/config.env`](../clickhouse/scripts/config.env) | Frozen semantic constants |
| [`clickstack/`](../clickstack/) | ClickStack / OTLP setup |

## Cloud vs self-host

| | Langfuse Cloud | Self-host |
|---|----------------|-----------|
| **Setup** | [cloud.langfuse.com](https://cloud.langfuse.com) — free tier | `clickhouse/scripts/setup_langfuse_selfhost.sh` |
| **Ops** | None | Postgres + ClickHouse + Redis + MinIO |
| **Use when** | Hackathon, fast start | Strict residency / high volume |

**Both are available.** Cloud is recommended unless you need on-prem.

## Architecture (best practice)

Direct upstream LiteLLM calls **bypass Langfuse** unless that proxy has callbacks
configured. Pulse can insert a **traced local LiteLLM proxy**:

```
LibreChat → LiteLLM (:4000, Langfuse callbacks) → upstream LiteLLM → models
                ↓
         Langfuse Cloud
```

Or point LibreChat **directly** at your org LiteLLM URL (no local proxy) — chat
works; Langfuse traces only if the upstream proxy is configured for your project.

**macOS + Podman + corp TLS:** run LiteLLM **on the host** if the container cannot
verify Langfuse HTTPS. Sync can point LibreChat at the Podman gateway IP.

```
./clickhouse/scripts/run_litellm.sh          # host terminal (keep running)
./clickhouse/scripts/sync_librechat_env.sh
podman-compose --profile chat up -d librechat clickhouse-mcp librechat-mongodb
```

On Linux, the in-compose `litellm` service works as-is.

Configured in `litellm/litellm-config.yaml`:
- `success_callback` / `failure_callback`: `["langfuse"]`
- Tags: `pulse`, `librechat`, `concurrency-agent`
- `LANGFUSE_TRACING_ENVIRONMENT` from `.env`

## Setup

1. Install the [Langfuse agent skill](https://github.com/langfuse/skills):

   ```bash
   clickhouse/scripts/install_langfuse_skill.sh
   ```

2. Add to root `.env` (gitignored — **never commit corp URLs or keys**):

   ```
   LANGFUSE_PUBLIC_KEY=pk-lf-...
   LANGFUSE_SECRET_KEY=sk-lf-...
   LANGFUSE_HOST=https://cloud.langfuse.com
   LITELLM_UPSTREAM_URL=https://your-org-litellm-host/v1
   LITELLM_BASE_URL=https://your-org-litellm-host/v1   # direct LibreChat mode
   LANGFUSE_TRACING_ENVIRONMENT=development
   export ANTHROPIC_API_KEY   # before sync
   ```

3. Sync and start chat stack:

   ```bash
   export ANTHROPIC_API_KEY
   ./clickhouse/scripts/sync_librechat_env.sh
   ./clickhouse/scripts/run_litellm.sh          # macOS: keep running in a terminal
   podman-compose --profile chat up -d librechat clickhouse-mcp librechat-mongodb
   ```

4. Chat in LibreChat (LiteLLM model + clickhouse MCP). Verify traces:

   ```bash
   ./clickhouse/scripts/verify_langfuse_traces.sh
   ```

## vs ClickStack

| Layer | Tool | Observes |
|-------|------|----------|
| API / pipeline | ClickStack | `concurrency.chart`, `loadraw`, … |
| Conversational | Langfuse | LLM generations, tool calls |

## Skill workflow

The installed `.cursor/skills/langfuse` skill follows Langfuse best practices:
descriptive trace names, generation types, tags, environment separation, and
post-setup trace audit via `verify_langfuse_traces.sh`.
