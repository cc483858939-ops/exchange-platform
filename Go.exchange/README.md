# Go.exchange

Go.exchange is a Go + Gin backend for article publishing, article reactions, recommendation signals, exchange-rate records, and article-cover file storage. The current local stack uses PostgreSQL, Redis, MinIO, and Docker Compose. Background work is split into a dedicated worker process, and schema changes are run through a one-shot migration job instead of API startup.

## Tech Stack

- Web framework: Gin
- ORM / database: GORM + PostgreSQL
- Cache and async state: Redis + Lua scripts
- Object storage: MinIO
- Authentication: JWT access tokens and Redis-backed refresh tokens
- Background workers: like-count persistence and article analysis workers
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
- `GET /api/recommendations/articles`
- `POST /api/uploads/article-cover`
- `POST /api/articles`
- `GET /api/articles`
- `GET /api/articles/:id`
- `GET /api/articles/:id/like`
- `PUT /api/articles/:id/like`
- `DELETE /api/articles/:id/like`

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

- Redis is the hot path for article like counts; PostgreSQL stores the durable projection.
- Article list/detail responses are cached in Redis.
- Article analysis is queued through Redis sets and processed by worker goroutines.
- Article cover images are stored in MinIO and served through `/api/files/*objectKey`.
- API and worker processes do not run `AutoMigrate`; schema changes belong to the `migrate` job.
