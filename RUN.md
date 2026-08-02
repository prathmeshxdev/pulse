# RUN.md — Pulse

Commands and configuration to reproduce the pipeline, run the dashboard locally,
and deploy the public demo. Prerequisites are listed in [README.md](./README.md#how-to-run-it).

Supported on **macOS** and **Linux** (bash). On Windows, use **WSL2**.

---

## Local pipeline (ClickHouse Cloud)

Copy [`.env.example`](.env.example) to `.env` and set `CLICKHOUSE_DSN` to the Cloud
native secure URL (`:9440/sony_liv?secure=true`).

```bash
export $(grep -v '^#' .env | xargs)
cd backend

# Schema
go run ./cmd/pipeline -dsn "$CLICKHOUSE_DSN" -migrations ../clickhouse/migrations -reload-dict

# Load + build
go run ./cmd/loadraw        -in ../hackathon-data/data/ch-hackathon-raw-data.csv -dsn "$CLICKHOUSE_DSN"
go run ./cmd/loadcontent    -in ../hackathon-data/data/ch-hackathon-content-data.csv -dsn "$CLICKHOUSE_DSN"
go run ./cmd/build_segments -in ../hackathon-data/data/ch-hackathon-raw-data.csv -dsn "$CLICKHOUSE_DSN" -segments= -deltas=
go run ./cmd/build_user_segments -dsn "$CLICKHOUSE_DSN" -config ../clickhouse/scripts/config.env

# Evidence
go run ./cmd/bench -dsn "$CLICKHOUSE_DSN" \
  -spec ../clickhouse/queries/benchmark/spec.example.json -out ../evidence
go run ./cmd/validate -dsn "$CLICKHOUSE_DSN" \
  -in ../hackathon-data/data/ch-hackathon-raw-data.csv

# API + UI
CLICKHOUSE_DSN="$CLICKHOUSE_DSN" PREFLIGHT_ENABLED=false go run ./cmd/server
# separate shell:
cd ../frontend && npm install && npm run dev
```

Local URLs: dashboard `http://localhost:5173`, API `http://localhost:8080`.

---

## Docker (local)

With `CLICKHOUSE_DSN` in `.env`:

```bash
docker compose up -d --build backend frontend
```

Set `PREFLIGHT_ENABLED=false` in `.env` to run without relying on Redis preflight.
The dashboard nginx container proxies `/api` to the backend service
([`frontend/nginx.conf`](frontend/nginx.conf)).

| Profile | Services |
|---------|----------|
| _(default)_ | `backend`, `frontend`, `redis` |
| `observability` | `clickstack` (OTLP → ClickHouse Cloud) |
| `chat` | LibreChat, pulse MCP, ClickHouse MCP, LiteLLM, Mongo |
| `full` | All integration services |
| `local` | Embedded ClickHouse (dev only) |

Batch ingest (`loadraw`, `build_segments`, `unseen_day.sh`) runs via the Go CLI on
the host; containers serve the API and UI against data already in Cloud.

**Verification:**

```bash
curl -sf http://localhost:8080/health
curl -sf http://localhost:8080/api/v1/schema/dimensions
./clickhouse/scripts/smoke_integrations.sh
```

---

## Unseen evaluation day

Full pipeline for the sealed evaluation dataset:

```bash
./clickhouse/scripts/unseen_day.sh /path/to/raw.csv /path/to/content.csv
```

Output: [`evidence/unseen_day/`](evidence/unseen_day/) (`answers.json`, consistency,
query log, sensitivity, run log).

---

## Environment variables

| Variable | Role |
|----------|------|
| `CLICKHOUSE_DSN` | ClickHouse Cloud connection (required) |
| `ADDR` | API listen address (default `:8080`) |
| `PREFLIGHT_ENABLED` | Redis-backed query cache (default `true`; `false` for minimal deploy) |
| `REDIS_ADDR` | Redis for preflight / live state |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | ClickStack OTLP HTTP endpoint |
| `LANGFUSE_PUBLIC_KEY` / `SECRET_KEY` / `HOST` | Langfuse via LiteLLM |
| `LITELLM_BASE_URL` | LibreChat custom LLM endpoint |
| `VITE_API_TARGET` | Frontend dev proxy target |

Secrets live in gitignored `.env`, `observability.env`, and
`librechat/librechat.runtime.yaml`. Redacted templates: [`.env.example`](.env.example).

---

## Observability and chat

Generate integration env from the root DSN:

```bash
./clickhouse/scripts/sync_librechat_env.sh
docker compose --profile observability up -d clickstack
docker compose --profile chat up -d
```

| Service | Local URL |
|---------|-----------|
| Dashboard | http://localhost:5173 |
| API | http://localhost:8080 |
| LibreChat | http://localhost:3080 |
| ClickStack OTLP | http://127.0.0.1:4318 |

ClickStack dashboard views: [`clickstack/dashboards.sql`](clickstack/dashboards.sql)
(applied once in ClickHouse Cloud).

---

## Deployment

Pulse keeps **ClickHouse Cloud** as the datastore. The public demo deploys the
**Go API** and **React UI** on a single origin — nginx serves the SPA and proxies
`/api` to the backend.

Production env on the host:

```bash
CLICKHOUSE_DSN=clickhouse://pulse_readonly:...@....clickhouse.cloud:9440/sony_liv?secure=true
PREFLIGHT_ENABLED=false
```

The API uses the read-only user from
[`clickhouse/scripts/create_readonly_user.sql`](clickhouse/scripts/create_readonly_user.sql)
(SELECT on serving tables only).

### Railway

Railway does **not** run `docker-compose.yml` from the repo root. A single service
at `/` makes Railpack scan the monorepo and fail with *“could not determine how to
build the app”*. Deploy **two services** from the same GitHub repo, each with its
own root directory and Dockerfile.

1. [Railway](https://railway.com) → **New Project** → **Deploy from GitHub repo** → `pulse`, branch `main`.
2. **Delete** the auto-created root service (or change it — see step 3).
3. **+ New → GitHub Repo** (same repo) twice — create two services:

| Service name | Root directory | Config file path | Public domain |
|--------------|----------------|------------------|---------------|
| `backend` | `/backend` | `/backend/railway.toml` | **Off** (private only) |
| `frontend` | `/frontend` | `/frontend/railway.toml` | **On** → port **80** |

Set these under each service → **Settings** → **Build** (Root Directory, Config file path).

4. **Backend variables:**

| Variable | Value |
|----------|--------|
| `CLICKHOUSE_DSN` | `clickhouse://pulse_readonly:...@....clickhouse.cloud:9440/sony_liv?secure=true` |
| `PREFLIGHT_ENABLED` | `false` |
| `ADDR` | `:8080` |

5. **Frontend variables** (name the API service `backend`):

| Variable | Value |
|----------|--------|
| `BACKEND_HOST` | `${{backend.RAILWAY_PRIVATE_DOMAIN}}:8080` |

6. Deploy **backend** first, then **frontend**. Verify:

```bash
curl -sf https://YOUR-APP.up.railway.app/health
curl -sf https://YOUR-APP.up.railway.app/api/v1/schema/dimensions
```

ClickHouse stays on **ClickHouse Cloud** — load data locally with
[`unseen_day.sh`](clickhouse/scripts/unseen_day.sh) before pointing the demo at Cloud.

**If you still see Railpack errors:** open the service → **Settings** → **Build** →
confirm **Builder** is **Dockerfile** (the `railway.toml` in each subdirectory
forces this). Root directory must be `/backend` or `/frontend`, not `/`.

### Coolify Cloud

Pulse deploys via [`docker-compose.coolify.yml`](docker-compose.coolify.yml) (API + dashboard only).

1. [Coolify Cloud](https://coolify.io/docs/get-started/cloud) — create an account and connect a VPS over SSH (Hetzner, DigitalOcean, etc.).
2. **New Resource → Docker Compose** — source: GitHub repo `prathmeshxdev/pulse`, branch `main`.
3. **Docker Compose location:** `docker-compose.coolify.yml` · **Base directory:** `/`
4. **Environment variables:** `CLICKHOUSE_DSN` (required), `PREFLIGHT_ENABLED=false`
5. Assign the generated domain to the **`frontend`** service (container port **80**).
6. Deploy; verify `https://<domain>/health` and the dashboard curve.

### Docker Compose on a Linux host

```bash
git clone https://github.com/prathmeshxdev/pulse.git
cd pulse && cp .env.example .env
docker compose up -d --build backend frontend
```

HTTPS is provided by a reverse proxy (Caddy, nginx + Let's Encrypt) or
[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/):

```bash
cloudflared tunnel --url http://localhost:5173
```

### Self-hosted PaaS

Pulse ships as a standard Compose stack and deploys on open-source platforms:

- **[Railway](https://railway.com)** — two services (`/backend`, `/frontend`); see [Railway](#railway) above.
- **[Coolify](https://coolify.io)** — Docker Compose from Git; env vars `CLICKHOUSE_DSN`, `PREFLIGHT_ENABLED=false`; automatic HTTPS.
- **[CapRover](https://caprover.com)** — same Compose stack on the VPS, or separate Dockerfile apps with `/api` proxied to one hostname.
- **[Dokku](https://dokku.com)** — `backend/` and `frontend/` apps with nginx routing `/api` to the API service.

### Deployment scope

| Component | Where it runs |
|-----------|---------------|
| ClickHouse | ClickHouse Cloud (serving + `otel_*` tables) |
| Batch pipeline | Host CLI (one-time or `unseen_day.sh`) |
| Dashboard + API | Deployed container stack |
| LibreChat / LiteLLM | Local or optional second stack (`--profile chat`) |
| ClickStack collector | Local OTLP forwarder to Cloud |

**Production checks:**

```bash
curl -sf https://<demo-host>/health
curl -sf https://<demo-host>/api/v1/schema/window
```

The chart uses the window returned by `/api/v1/schema/window` over data loaded in Cloud.

---

## CLI reference

| Command | Purpose |
|---------|---------|
| `go run ./cmd/pipeline …` | Migrations |
| `go run ./cmd/loadraw …` | CSV → `raw_events` |
| `go run ./cmd/loadcontent …` | Content → `content_metadata` |
| `go run ./cmd/build_segments …` | Session segments + minute deltas |
| `go run ./cmd/build_user_segments …` | User-grain tables |
| `go run ./cmd/bench …` | Benchmark → `evidence/` |
| `go run ./cmd/validate …` | Invariants + sensitivity |
| `./clickhouse/scripts/unseen_day.sh` | Sealed-day full pipeline |
| `./clickhouse/scripts/replay.sh` | Incremental watermark replay |
| `./clickhouse/scripts/start_chat_fresh.sh` | Reset LibreChat stack |

Commands under `go run` execute from `backend/` unless noted.

---

## Incremental replay

```bash
./clickhouse/scripts/replay.sh <raw.csv> <watermark_epoch_ms>
```

Replays events up to a watermark so open sessions and reconcile appear in the Replay view.
