# Development Runtime

This project has separate runtime roles for the HTTP API and background workers. Use Docker Compose to manage long-running services instead of entering a container and running `go run main.go` manually.

## Services

- `migrate`: runs database schema migration once, then exits.
- `api`: runs the HTTP API only and exposes port `3000`.
- `worker`: runs background jobs only, including like-count sync and article analysis.
- `db`: PostgreSQL.
- `redis`: cache, refresh tokens, like hot state, and worker queues.
- `minio`: article-cover object storage.
- `frontend`: Vite development server for the sibling frontend project.
- `prometheus`, `grafana`, `kafka-ui`: optional observability/admin services.

Kafka is present in the compose stack for local experiments and future event-driven work. The current backend async workflow mainly uses Redis.

## Backend

Start the backend API, worker, frontend, and common dependencies:

```powershell
cd D:\code\mf
docker compose up -d api worker db redis kafka minio frontend
```

The `api` and `worker` services depend on the one-shot `migrate` service, so schema migration runs before they start. To run it explicitly:

```powershell
cd D:\code\mf
docker compose run --rm migrate
```

Optional observability and admin services:

```powershell
docker compose up -d prometheus grafana kafka-ui
```

Check the API:

```powershell
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:3000/healthz
```

## Frontend

The root compose file can run the Vite frontend service. Open:

```text
http://127.0.0.1:5173
```

The frontend proxies `/api` requests to the backend API service.

## Runtime Roles

The Go binary reads `APP_RUNTIME_ROLE`.

- `APP_RUNTIME_ROLE=api`: starts only the HTTP API.
- `APP_RUNTIME_ROLE=worker`: starts only background workers.
- unset or `APP_RUNTIME_ROLE=all`: starts both API and workers in one process.

For normal Compose development, use the split `api` and `worker` services. Do not run schema migration from API or worker startup paths; use the `migrate` service instead.

## Observability URLs

Backend endpoints:

- Health: `http://127.0.0.1:3000/healthz`
- Readiness: `http://127.0.0.1:3000/readyz`
- Metrics: `http://127.0.0.1:3000/metrics`

Dashboards and admin UIs:

- Prometheus: `http://127.0.0.1:9090`
- Prometheus targets: `http://127.0.0.1:9090/targets`
- Grafana: `http://127.0.0.1:3001`
- Go.exchange Grafana dashboard: `http://127.0.0.1:3001/d/go-exchange-overview`
- MinIO Console: `http://127.0.0.1:9001`
- Kafka UI: `http://127.0.0.1:8080` when `kafka-ui` is running

Internal pprof endpoints:

- API container: `http://api:6060/debug/pprof/`
- Worker container: `http://worker:6060/debug/pprof/`

The local compose file does not currently publish API/worker pprof port `6060` to the host. Do not expect `http://127.0.0.1:6060/debug/pprof/` to work unless the compose ports are changed.

## Notes

The Compose file mounts named Go module and build-cache volumes for the backend services, so the first container start may download dependencies, but later restarts should be faster.
