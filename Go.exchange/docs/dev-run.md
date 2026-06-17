# Development Runtime

This project has separate runtime roles for the HTTP API and background workers.
Use Docker Compose to manage long-running services instead of entering a
container and running `go run main.go` manually.

## Services

- `api`: runs the HTTP API only and exposes port `3000`.
- `worker`: runs background jobs only, including like-count sync and article analysis.
- `app`: devcontainer shell service only. It is behind the `devcontainer` profile and does not run the backend API.

## Backend

Start the backend API, worker, and dependencies:

```powershell
cd D:\code\mf\Go.exchange
docker compose up -d api worker db redis kafka
```

Optional observability services:

```powershell
docker compose up -d prometheus grafana kafka-ui
```

Check the API:

```powershell
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:3000/healthz
```

## Frontend

Run the Vite frontend from the sibling project:

```powershell
cd D:\code\mf\Exchangeapp_frontend
npm run dev -- --host 0.0.0.0
```

Open:

```text
http://127.0.0.1:5173
```

The frontend proxies `/api` requests to `http://127.0.0.1:3000`.

## Runtime Roles

The Go binary reads `APP_RUNTIME_ROLE`.

- `APP_RUNTIME_ROLE=api`: starts only the HTTP API.
- `APP_RUNTIME_ROLE=worker`: starts only background workers.
- unset or `APP_RUNTIME_ROLE=all`: starts both API and workers in one process.

For normal Compose development, use the split `api` and `worker` services.

The Compose file mounts named Go module and build-cache volumes for the backend
services, so the first container start may download dependencies, but later
restarts should be faster.
