# Authenticated k6 read-heavy load test

This directory contains the authenticated, read-only k6 load-test foundation for
the Exchange API. It is intended for local validation of request behavior and
observability wiring, not as a production SLO or capacity claim.

## Scope and safety

- `cmd/loadtest-users` creates or updates only deterministic synthetic users.
- The k6 setup phase logs in sequentially once per synthetic user and keeps the
  resulting access-token pool in memory.
- The workload performs recommendation, post-detail, following,
  notifications, and bookmark reads. It does not register users or perform
  likes, bookmarks, follows, posts, replies, or other mutations.
- Passwords, access tokens, response bodies, and upstream authentication errors
  are not printed by the seeder or test script.
- The thresholds are local baseline guards only:
  `http_req_failed < 1%` and `http_req_duration p95 < 500ms`.

The existing mutation-oriented scripts in this directory are kept unchanged.

## 1. Provision synthetic users

Run from the repository root so the command uses the same Compose network and
database configuration as the API container:

```powershell
Set-Location D:\code\mf
$env:LOADTEST_USER_PASSWORD = "use-a-local-test-password"
docker compose run --rm -e LOADTEST_USER_PASSWORD -e LOADTEST_USER_COUNT=10 -e LOADTEST_USER_PREFIX=loadtestv1 api go run ./cmd/loadtest-users
```

The password is required and has no default. The default count is `10`, with a
hard limit of `1..20`; the default prefix is `loadtestv1`. Usernames are
`loadtestv1_0001` through `loadtestv1_0010` and display names are
`Load Test User 0001` through `Load Test User 0010`.

Rerunning the command is safe: existing exact synthetic usernames are updated,
soft-deleted exact targets are restored, and unrelated users are not restored
or modified. The ensure operation is one database transaction.

## 2. Run k6

Run from `Go.exchange` against a running API. `BASE_URL` defaults to
`http://127.0.0.1:3000`.

```powershell
Set-Location D:\code\mf\Go.exchange
$env:LOADTEST_USER_PASSWORD = "use-a-local-test-password"
$env:LOADTEST_PROFILE = "smoke"
k6 run .\loadtest\read-heavy.js
```

The supported profiles are:

- `smoke`: 1 VU for 30 seconds.
- `baseline` (default): 30s to 5 VUs, 60s to 20 VUs, 60s to 50 VUs, then
  30s back to 0.

Override the endpoint, synthetic-user pool, or profile when needed:

```powershell
$env:BASE_URL = "http://127.0.0.1:3000"
$env:LOADTEST_USER_COUNT = "10"
$env:LOADTEST_USER_PREFIX = "loadtestv1"
$env:LOADTEST_PROFILE = "baseline"
k6 run .\loadtest\read-heavy.js
```

Before the VU stages begin, the script checks `/healthz` and logs in each
synthetic user sequentially. A `429` during this phase fails immediately and
reports only the `Retry-After` value, if present. Tokens are assigned by
`(__VU - 1) % token_count`, so no VU logs in during an iteration.

## 3. Grafana

The dedicated dashboard is named **Go.exchange Load Test** and has UID
`go-exchange-loadtest`. It uses the existing Prometheus datasource and existing
metrics only; this change does not add an exporter, a container, or a new
Prometheus/Grafana service.

Open the dashboard in the existing Grafana instance and use the default
`now-15m` time range with a 15-second refresh. It covers HTTP QPS, overall and
per-route latency/error rate, recommendation generation/outcomes, worker
backlog/health, and notification consumer lag.
