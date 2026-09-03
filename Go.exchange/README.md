# Go.exchange

Go.exchange is a Go + Gin backend for unified Post publishing, Post reactions, recommendation signals, exchange-rate records, and long-form cover file storage. The current local stack uses PostgreSQL, Redis, MinIO, and Docker Compose. Background work is split into a dedicated worker process, and schema changes are run through a one-shot migration job instead of API startup.

## Tech Stack

- Web framework: Gin
- ORM / database: GORM + PostgreSQL
- Cache and async state: Redis + Lua scripts
- Object storage: MinIO
- Authentication: JWT access tokens and Redis-backed refresh tokens
- Background workers: like-count persistence, recommendation projection, nearline profile materialization, and Kafka-first Post embedding consumption
- Observability: Prometheus metrics, Grafana dashboard, health checks, pprof
- Local orchestration: Docker Compose

Kafka and Kafka UI are present in the local compose file for experiments or future event-driven work, but the current backend request and worker paths mainly use Redis rather than Kafka.

## Runtime Roles

The same Go application can run different roles through `APP_RUNTIME_ROLE`:

- `api`: HTTP API only
- `worker`: background workers only
- unset or `all`: API and workers in one process

Normal local development should use the split `api` and `worker` compose services.

## Database Migration

Schema migration is intentionally separated from API and worker startup.

- Migration entrypoint: `cmd/migrate`
- Migration runner: `initialize.RunMigrations()`
- Compose service: `migrate`

The `api` and `worker` services depend on `migrate` completing successfully. `migrate` is a one-shot container, so `Exited (0)` is the expected successful state.

Manual migration command:

```powershell
cd D:\code\mf
docker compose run --rm migrate
```

Runtime readiness is evaluated from an immutable two-second snapshot. `/healthz`
is liveness-only; `/readyz` gates API startup on PostgreSQL, the published
`runtime_schema_state` compatibility interval and the Redis ping. Kafka is
reported as degraded for API readiness and does not make the API endpoint
return 503. Worker readiness additionally reports each required pipeline's
active workers, last commit, failures and backlog. Readiness snapshots are
fail-closed when their provider is unavailable or stale; the evaluator's
timestamp and pipeline backlog-stall gauges are exported for alerting.

For Kubernetes, run `k8s/deploy.ps1` from any directory with a lowercase
release revision and an immutable image digest:

```powershell
.\k8s\deploy.ps1 `
  -Namespace default `
  -ReleaseRevision 09eecd4b7f86 `
  -Image registry.example.com/go-exchange@sha256:<64-hex-digest>
```

The script renders the manifests with the supplied image and revision, creates
a new generated-name migration Job, verifies that exact Job name and UID, and
only then applies and rolls out the API and Worker. It never applies the
migration manifest and it does not build or push images. Kubernetes acceptance
is not claimed unless this script has been run against a real cluster.

## Recommendation profile materialization

The For You endpoint reads `user_reco_profiles` by user primary key. View,
reaction, feedback, reply, and actual Post-embedding changes invalidate the
durable `user_reco_profile_dirty` queue in their source transactions. The
worker rebuilds canonical `user_post_reco_states`, nullable pgvector
interest profiles, and raw candidate-author affinity atomically. A compatible
stale profile remains usable while recovery is queued; misses and incompatible
profiles use cold start.

The sections below describe the current materialized-profile and
recommendation-serving behavior implemented by this backend.

## Recommendation serving pipeline

The authenticated For You path uses the following serving stages:

```text
Multi-source Recall (semantic, following, recent, trending)
        ↓
Equal Reciprocal Rank Fusion
        ↓
Rule-based Multi-signal Ranking
        ↓
Diversity / Network Balance / Exploration
```

Equal RRF controls candidate admission into the bounded pool; its score and
source count are not part of the final ranking `BaseScore`. Positive semantic
relevance is computed from the hydrated candidate embedding for every
candidate comparable with the user's positive profile, regardless of recall
source. Recency is handled by Recent recall, publication-time tie breaking,
and exploration/selection policies rather than a freshness `BaseScore`
component.

## Local Development

The local development prerequisites are Docker, Docker Compose, and Go 1.25+ for first-time JWT key generation. Docker-only key generation has not been verified here, so the documented fallback runs the generator with host Go:

```powershell
cd D:\code\mf\Go.exchange
go run ./cmd/gen-jwt-keys --kid local-dev-v1 --out .secrets/jwt
```

The Docker development image exposes the generated host files at the paths used by `Dockerfile.dev`. Worker mode does not load JWT configuration.

`Go.exchange/.env.example` is a reference template only; root Compose does not load it automatically. When running Compose from `D:\code\mf`, Docker Compose can take overrides from the shell environment, the repository-root `.env`, or an explicit `--env-file`. The `DATABASE_DSN` default in `docker-compose.yml` is used when no override is supplied.

Start the common local stack from the repository root:

```powershell
cd D:\code\mf
docker compose up -d api worker db redis kafka minio frontend
```

Optional observability and admin UI services:

```powershell
docker compose up -d prometheus grafana kafka-ui
```

Health check:

```powershell
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:3000/healthz
```

## Useful Local URLs

- Frontend: `http://127.0.0.1:5173`
- API health: `http://127.0.0.1:3000/healthz`
- API readiness: `http://127.0.0.1:3000/readyz`
- API metrics: `http://127.0.0.1:3000/metrics`
- Prometheus: `http://127.0.0.1:9090`
- Grafana: `http://127.0.0.1:3001`
- MinIO Console: `http://127.0.0.1:9001`
- Kafka UI: `http://127.0.0.1:8080` when the optional `kafka-ui` service is running

The API and worker both start pprof on port `6060` inside their containers. That port is not currently published to the host in the local compose file.

## Core API Surface

Public endpoints:

- `POST /api/auth/login`
- `POST /api/auth/register`
- `POST /api/auth/refresh`
- `GET /api/exchangeRates`
- `GET /api/files/*objectKey`

Authenticated endpoints:

- `POST /api/exchangeRates`
- `GET /api/recommendations/posts`
- `POST /api/uploads/post-media`
- `POST /api/posts`
- GET /api/feed/following?limit=20&cursor=...
- GET /api/users/:id/posts?limit=20&cursor=...
- `GET /api/posts/:id`
- `GET /api/posts/:id/replies?limit=20&cursor=...`
- `DELETE /api/posts/:id`
- `GET /api/posts/:id/like`
- `PUT /api/posts/:id/like`
- `DELETE /api/posts/:id/like`

Following and user-post endpoints return {"items":[],"next_cursor":null}; cursor values are opaque.

## Project Layout

```text
Go.exchange/
|-- cmd/migrate/       # one-shot database migration command
|-- config/            # config loading and runtime dependency initialization
|-- consts/            # Redis keys and Lua scripts
|-- controllers/       # HTTP handlers
|-- core/              # HTTP server startup and graceful shutdown
|-- global/            # shared DB, Redis, and MinIO clients
|-- initialize/        # app initialization and migration runner
|-- metrics/           # Prometheus metrics middleware and handler
|-- middlewares/       # JWT auth middleware
|-- models/            # GORM models
|-- observability/     # Prometheus and Grafana provisioning
|-- router/            # route registration
|-- tasks/             # background workers
|-- utils/             # JWT and utility helpers
`-- main.go            # API/worker runtime entrypoint
```

## Design Notes

- Redis is the hot path for Post like counts; PostgreSQL stores the durable projection.
- Post detail responses are cached in Redis.
- Post embedding analysis is queued through Redis sets and processed by worker goroutines.
- Long-form Post cover images are stored in MinIO and served through `/api/files/*objectKey`.
- API and worker processes do not run `AutoMigrate`; schema changes belong to the `migrate` job.
