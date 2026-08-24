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

JWT_ACTIVE_KID=jwt-2026-01
JWT_PRIVATE_KEY_B64=<base64-encoded-ed25519-pkcs8-private-pem>
JWT_ISSUER=go.exchange
JWT_AUDIENCE=go.exchange.api
JWT_ACCESS_TTL=15m
JWT_REFRESH_IDLE_TTL=168h
JWT_REFRESH_ABSOLUTE_TTL=720h
JWT_CLOCK_SKEW=30s

# Optional: comma-separated direct proxy IPs/CIDRs for client-IP headers.
# TRUSTED_PROXY_CIDRS=<confirmed-direct-proxy-cidr>
```

Generate the key pair outside Railway and never commit it:

```powershell
go run ./cmd/gen-jwt-keys --kid jwt-2026-01 --out .secrets/jwt
[Convert]::ToBase64String([IO.File]::ReadAllBytes('.secrets/jwt/private.pem'))
```

Store the Base64 result as a Railway secret. Do not give the JWT private key
to Worker or migration services. `JWT_VERIFY_KEYS_B64` is optional and is only
needed while old public keys remain valid during a future rotation window.

`TRUSTED_PROXY_CIDRS` defines the IPs or CIDRs of proxies that connect directly
to the API and are therefore allowed to provide `X-Forwarded-For` or
`X-Real-IP`. When it is empty, all forwarding headers are ignored. Production
must use the directly connecting Railway or Kubernetes proxy ranges confirmed
from the deployed network topology; this document does not assume a fixed
Railway CIDR. Do not configure `0.0.0.0/0` or `::/0`. Invalid proxy
configuration causes the API to reject startup. Without a trusted CIDR, rate
limiting still works, but requests sharing one proxy can share the IP bucket.

Use the actual Railway service names in reference variables. Do not set `PORT`:
the application reads the port that Railway supplies.

After deployment, `GET /healthz` must return HTTP 200. The public root path is
not a frontend page; it is an API service and may return HTTP 404.
