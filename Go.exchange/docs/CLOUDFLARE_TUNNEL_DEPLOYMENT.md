# Cloudflare Tunnel deployment for a locally hosted API

This runbook exposes the API running on the owner's Windows computer through a
Cloudflare Named Tunnel while the Vue application remains on Cloudflare Pages.
It is designed for the active Cloudflare DNS zone `ccyu.dpdns.org`.

## Request path

```text
Browser -> Pages /api/* -> Pages Function -> api.ccyu.dpdns.org/api/*
        -> Cloudflare Named Tunnel -> http://127.0.0.1:3000 -> Go API
```

The Pages Function appends the matched path after `/api` to `API_ORIGIN`.
Therefore the Cloudflare Pages production variables must be:

```text
API_ORIGIN=https://api.ccyu.dpdns.org/api
VITE_API_BASE_URL=/api
```

The tunnel's local service must be `http://127.0.0.1:3000` without `/api`.
For example, a browser request to `/api/auth/login` reaches the Go route
`/api/auth/login`, not `/api/api/auth/login`.

## Local services

Run the API with its independent local data services from the monorepo root:

```powershell
cd D:\code\mf
docker compose up -d db redis minio kafka api worker
curl.exe http://127.0.0.1:3000/healthz
```

All Compose host ports are deliberately bound to `127.0.0.1`. Cloudflare
Tunnel is the only public ingress. Do not add router port-forwarding rules for
the API, PostgreSQL, Redis, MinIO, Kafka, Prometheus, or Grafana.

The local PostgreSQL and MinIO data are independent from Railway. A Railway
data migration is a separate, deliberate operation and is not part of this
deployment procedure.

## Cloudflare account steps

These steps require Cloudflare account access and must be completed by the
account owner.

1. In **Networking -> Tunnels**, create a remotely-managed Named Tunnel named
   `go-exchange-home-api`.
2. Download `cloudflared` to `D:\tools\cloudflared\` and use the Windows
   installation command shown by Cloudflare to run it as a Windows service.
   Keep the tunnel token secret and out of Git.
3. On that tunnel, add a Published application:

   ```text
   Public hostname: api.ccyu.dpdns.org
   Service type: HTTP
   URL: http://127.0.0.1:3000
   ```

   Cloudflare should create a proxied CNAME for `api` to the tunnel hostname.
4. Before Access is enabled, confirm both of these work:

   ```text
   https://api.ccyu.dpdns.org/healthz
   https://<pages-production-host>/api/exchangeRates
   ```

5. Set the two Pages production variables above and deploy Pages. Keep the
   current Railway `API_ORIGIN` value recorded as the rollback target.

## Protect the upstream API with Cloudflare Access

After basic forwarding works, create an Access Application for
`api.ccyu.dpdns.org` and add a **Service Auth** policy for one new Service
Token. Save these as Pages secrets, not `VITE_` build variables:

```text
CF_ACCESS_CLIENT_ID
CF_ACCESS_CLIENT_SECRET
```

The Pages Function conditionally adds those two headers to upstream requests.
It remains backward compatible before the secrets exist, but fails closed if
only one secret is configured. It also removes same-named browser headers so
the upstream identity can only originate from the Pages secret binding.

When Access is active, direct requests to `api.ccyu.dpdns.org` should be
denied, while Pages `/api/*` requests continue to work. The Go API's JWT
authentication remains responsible for user identity; additionally rate-limit
login, registration, and upload routes in Cloudflare.

## Operations and rollback

- Keep the Windows computer awake and Docker Desktop configured to start.
- Check the Tunnel dashboard for a **Healthy** connector after reboots.
- Back up the local PostgreSQL and MinIO volumes regularly before relying on
  this deployment for demonstrations.
- If the local host fails, set `API_ORIGIN` back to the saved Railway value and
  redeploy Pages. No frontend code change is necessary.
