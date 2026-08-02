# Railway deployment

This directory is the backend service root. In Railway, configure the backend
service with the following settings:

- **Root Directory:** `/Go.exchange`
- **Config File:** `/Go.exchange/railway.toml`
- **Start Command:** leave empty, so the Dockerfile entrypoint runs.

The default `Dockerfile` is the production image. `Dockerfile.dev` is retained
for local Docker Compose and devcontainer workflows.

## Required services and variables

Create PostgreSQL, Redis, and an S3-compatible MinIO service before deploying
the API. The backend starts by connecting to all three services.

```env
APP_RUNTIME_ROLE=api
DATABASE_DSN=${{Postgres.DATABASE_URL}}
REDIS_ADDR=${{Redis.REDISHOST}}:${{Redis.REDISPORT}}
REDIS_PASSWORD=${{Redis.REDISPASSWORD}}
REDIS_DB=0
MINIO_ENDPOINT=<private-minio-host>:9000
MINIO_ACCESS_KEY=<minio-access-key>
MINIO_SECRET_KEY=<minio-secret-key>
MINIO_BUCKET=go-exchange
MINIO_USE_SSL=false
```

Use the actual Railway service names in reference variables. Do not set `PORT`:
the application reads the port that Railway supplies.

After deployment, `GET /healthz` must return HTTP 200. The public root path is
not a frontend page; it is an API service and may return HTTP 404.
