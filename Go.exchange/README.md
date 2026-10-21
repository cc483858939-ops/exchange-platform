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

## Runtime Binaries

The API and background workers run as separate executables and processes:

- API: `cmd/api` (HTTP API, API database, Redis, MinIO, JWT, and Kafka publisher)
- Worker: `cmd/worker` (worker database, Redis, worker tasks, and worker health server)

Local development runs both through the separate `api` and `worker` Compose services.

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

For per-process PostgreSQL pool defaults and multi-process connection budgeting,
see [the database connection budget runbook](docs/runbooks/database-connection-budget.md).

Runtime readiness is evaluated from an immutable two-second snapshot. `/healthz`
is liveness-only; `/readyz` gates API startup on PostgreSQL, the published
`runtime_schema_state` compatibility interval and the Redis ping. Kafka is
reported as degraded for API readiness and does not make the API endpoint
return 503. Worker `/readyz` also checks its required dependencies and reports
each required pipeline's active workers, last commit, failures and backlog.
Kafka commits clear pipeline failures after a real commit. A Kafka pipeline
with no new messages can recover a transport-only failure after a fresh
committed-lag sample reports zero and every local Reader is present in a
`Stable` consumer group subscribed to the configured topic. Lag alone cannot
clear processing, commit or configuration failures. Readiness snapshots are
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

Following, Recent, Trending, onboarding-interest Recent, and guest Public
Recent/Trending admit otherwise eligible public root posts within the configured
candidate window even when the post has no embedding for the active model.
Hydration keeps those posts with a nil embedding; semantic scores and semantic
exploration are zero for them, while social, trending, language, and other
non-semantic signals continue to rank them. Semantic recall remains isolated to
the active `ServingVersion`: it requires a valid, nonzero post vector for that
version and a compatible positive profile. If that vector disappears between
semantic recall and hydration or snapshot revalidation, the semantic candidate
is dropped rather than retaining stale similarity metadata. No older-version
vector is used as a fallback.

### Embedding model-version switch

`BuildVersion` selects the version written by the Post Embedding worker;
`ServingVersion` independently selects the version read by semantic recall and
candidate hydration. To switch models, configure the worker's
`EMBEDDING_BUILD_VERSION` for the target, allow it to fill target-version
embeddings, and run `go run ./cmd/check-post-embedding-coverage` to review the
target coverage for the configured candidate window. The coverage command is a
report only; it does not block a switch. Then update serving with
`go run ./cmd/set-embedding-serving-version --version <target-version>`.

Changing `ServingVersion` does not generate embeddings or rebuild profiles.
Profiles whose embedding version or configuration hash no longer matches are
treated as incompatible and their vectors are not served; non-semantic
candidates continue to be available while profile recovery rebuilds a compatible
profile. After the switch, verify the serving version and profile recovery
before expecting semantic recall to resume.

Authenticated For You pagination reuses a short-lived Redis candidate snapshot
for up to 120 seconds. The snapshot stores at most 500 lightweight ranked
candidates, not rendered Post responses. Every page rechecks current Post
eligibility before mapping the response. Cursors bind the viewer and page size,
are HMAC signed, and return `400 INVALID_CURSOR` for tampering or `410
CURSOR_EXPIRED` when their snapshot expires. Redis failures fall back to the
uncached recommendation path and return `next_cursor: null` with
`depleted: false`.

Set `RECOMMENDATION_CURSOR_SIGNING_KEY` to a private random secret containing
at least 32 bytes for each API deployment. Keep it outside source control and
do not expose it as a frontend `VITE_*` variable. If it is absent, the API does
not issue authenticated recommendation cursors. Snapshot outcomes are counted
by the bounded `go_exchange_recommendation_snapshot_total{outcome=...}` metric.

## Recommendation CTR and Click Deduplication

Recommendation CTR is **clicked opportunities / viewed opportunities**.
One opportunity is identified by the server-signed (user ID, recommendation
request ID, Post ID). For each opportunity, at most one impression and one click
contribute to the CTR counters. A click implies an impression when the user
opens a card before its one-second visibility threshold; a later impression
event cannot count it again. Clicks are attributed to their original impression
UTC date, including clicks across midnight.

The PostgreSQL `recommendation_ctr_claims` table atomically enforces those
business-level rules inside the same transaction as `ConsumerInbox`, the daily
metrics and the behavior projection. `event_id` remains the Kafka transport
idempotency key; refreshing it cannot increase CTR. Read-end, dwell and
not-interested events retain their own semantics, independent of CTR claims.

As an early duplicate filter, Redis reserves the same *opportunity* during
Click publication: a 60-second `PENDING` lease becomes a 5-minute
`COMMITTED` lease after Kafka publish succeeds. A **new** recommendation
request can count the same Post again immediately, while an old request can
never increase CTR after the Redis lease expires because the PostgreSQL claim
persists. The Redis layer is best-effort, not Kafka exactly-once. Its duration
is fixed at 300 seconds; no environment-variable override exists.

The durable claim ledger grows with recommendation opportunities. Before
high-volume production rollout, capacity-plan its primary-key index and table
growth; monitor storage and transaction latency. **Do not purge claims merely
because their Redis TTL has expired.** Keeping the ledger at least as long as
any Kafka replay or duplicate delivery window is necessary for durable CTR
deduplication.

Operational rebuild contract: stop recommendation metric Workers, choose and
record a consistent Kafka replay boundary, then reset
`recommendation_daily_metrics`, `recommendation_ctr_claims`, and the
recommendation-metrics ConsumerInbox rows **as one coordinated recovery
operation**. Preserve other consumer groups' inbox rows. Rebuilds that include
the behavior projection must also plan `post_behaviors` and profile-dirty
state explicitly; blindly replaying after resetting only metrics would
double-apply behavior. Restore Kafka consumption only after all reset steps
and the replay range are verified. This document is a recovery prerequisite,
not an automated production purge mechanism; retention must be implemented
and reviewed before scheduling destructive cleanup.

If a delayed real impression precedes an already-counted click across UTC
midnight, the worker moves the provisional CTR counts to the original
exposure day within the claim/inbox/metric transaction. Non-CTR behavior
counts and profile invalidation are not repeated.

## Local Development

The local development prerequisites are Docker, Docker Compose, and Go 1.25+ for first-time JWT key generation. Docker-only key generation has not been verified here, so the documented fallback runs the generator with host Go:

```powershell
cd D:\code\mf\Go.exchange
go run ./cmd/gen-jwt-keys --kid local-dev-v1 --out .secrets/jwt
```

The Docker development image exposes the generated host files at the paths used by `Dockerfile.dev`. The Worker executable does not load JWT configuration.

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

Pipeline health metrics (CDC slot/WAL lag, oldest retained Outbox age, profile
dirty queue and Redis like queue depths) are sampled every 10 seconds. Exact
retained Outbox and notification ConsumerInbox row counts are sampled at startup
and every 5 minutes in a separate loop. Each loop has a 5-second total sampling
budget. Failed counts retain the previous value and success timestamp;
`go_exchange_outbox_rows_last_success_timestamp_seconds` and
`go_exchange_consumer_inbox_rows_last_success_timestamp_seconds{consumer="…"}`
expose freshness. ConsumerInbox row count measures deduplication history, not
consumer lag. This changes no retention, cleanup, or readiness policy.

## Core API Surface

Public endpoints:

- `POST /api/auth/login`
- `POST /api/auth/register`
- `POST /api/auth/refresh`
- `POST /api/auth/logout`
- `GET /api/exchangeRates`
- `GET /api/files/*objectKey`

`POST /api/auth/logout` accepts `{"refresh_token":"rt1.…"}` and returns `204`
when that single refresh family is revoked or already absent. It also accepts a
consumed secret from the same family, so concurrent rotation cannot defeat
logout. It requires no Access JWT and never revokes other devices or a later
login. Already-issued Access JWTs remain valid until expiry plus configured clock
skew (defaults: 15 minutes and 30 seconds); replay detection has the same access
boundary. Access verification adds no Redis or database lookup. The browser
clears local credentials immediately; a network or server failure explicitly
reports that server sign-out is unconfirmed. No offline revocation guarantee is
provided. Used-secret and sealed recovery records expire under their existing
TTLs and cannot refresh an absent family.

Authenticated endpoints:

- `POST /api/exchangeRates`
- `GET /api/recommendations/posts`
- `POST /api/uploads/post-media`
- `POST /api/posts`
- GET /api/feed/following?limit=20&cursor=...
- GET /api/users/:id/timeline?limit=20&cursor=...
- `GET /api/posts/:id`
- `GET /api/posts/:id/replies?limit=20&cursor=...`
- `DELETE /api/posts/:id`
- `GET /api/posts/:id/like`
- `PUT /api/posts/:id/like`
- `DELETE /api/posts/:id/like`

Following and profile-timeline endpoints return {"items":[],"next_cursor":null}; cursor values are opaque. The profile timeline contains authored top-level posts and active reposts, ordered by activity time using cursor pagination.

## Project Layout

```text
Go.exchange/
|-- cmd/api/           # API runtime executable
|-- cmd/worker/        # Worker runtime executable
|-- cmd/migrate/       # one-shot database migration command
|-- config/            # side-effect-free config loading and dependency constructors
|-- consts/            # Redis keys and Lua scripts
|-- controllers/       # HTTP handlers
|-- core/              # HTTP server startup and graceful shutdown
|-- global/            # shared DB, Redis, and MinIO clients
|-- initialize/        # schema validation and migration runner
|-- metrics/           # Prometheus metrics middleware and handler
|-- middlewares/       # JWT auth middleware
|-- models/            # GORM models
|-- observability/     # Prometheus and Grafana provisioning
|-- router/            # route registration
|-- tasks/             # Worker background tasks
|-- utils/             # JWT and utility helpers
```

## Design Notes

- Redis is the hot path for Post like counts; PostgreSQL stores the durable projection.
- Post detail responses are cached in Redis.
- Post embedding work is requested through Kafka and consumed in bounded batches by a worker.
- Long-form Post cover images are stored in MinIO and served through `/api/files/*objectKey`.
- API and worker processes do not run `AutoMigrate`; schema changes belong to the `migrate` job.

## Post Embedding Consumer

The Kafka Post Embedding consumer collects up to `embedding.batch_size` messages
before calling the configured provider. The shipped configuration uses a batch
size of 5 and a maximum collection wait of 50 ms after the first message. Older
configuration files without these fields default to a batch size of 1 and a
50 ms maximum wait; size 1 skips collection waiting. Valid ranges are 1–32
messages and 1–1000 ms. `EMBEDDING_BATCH_SIZE` and
`EMBEDDING_BATCH_MAX_WAIT_MS` override the YAML values. Messages are processed
serially across batches, and Kafka offsets are committed only after every
message in that fetched batch is persisted, already current, missing, or
successfully sent to the DLQ. Provider failures use typed recovery actions:
HTTP 429 rate-limit errors and retryable server errors receive bounded retries
and then a category-specific cooldown, honoring a valid `Retry-After`. HTTP 429
quota exhaustion and authentication, permission, or endpoint failures pause
provider requests without entering the DLQ. Unrecognized HTTP 400/422 errors
pause for configuration and input review. HTTP 413 and Provider Contract Errors
may split a multi-message batch; an unresolved single-message provider error
remains uncommitted and pauses for recovery. Only confirmed Kafka message-level
permanent errors, such as invalid event envelopes, are sent to the DLQ.

## Worker Kafka Recovery

Kafka consumer health is tracked per stable worker slot. A successful commit
clears failures only for the slot that committed, and the pipeline failure
metric reports the maximum consecutive failure count across its required slots.
Readiness also requires an active, recovery-eligible Reader session for every
required slot and a fresh (30-second maximum age) background proof that each
Reader belongs to its configured `Stable` consumer group and subscribes to the
configured topic. Reader creation alone never makes a pipeline ready. Group
membership and committed Lag are sampled independently; failed or non-Stable
Group samples revoke the previous membership proof, while the `/readyz`
evaluator reads only the latest cached snapshot and never queries Kafka.
Cancelled Group samples are rejected both when their result is checked and at
the membership state write boundary, so they cannot refresh a proof or trigger
idle recovery.

A batch collection deadline is a normal end to the collection window. A
terminal Kafka fetch error lets the consumer finish and commit messages already
fetched, records the fetch failure, then closes that Reader so the supervisor
can create a new session. Shutdown cancellation does not create a transport
failure.

Committed Lag is sampled by a bounded pool with a five-second overall budget,
a four-second per-Group-query budget, and at most five concurrent groups. Lag
queries use the overall context independently of the Group-query timeout. Each
pipeline publishes its own sample. A successful Lag sample remains valid when
the consumer-group query fails, but that failure revokes readiness membership
and cannot trigger idle recovery. Idle recovery requires fresh zero Lag, the
same Stable Group membership proof used by readiness, and unchanged failure
and session versions. The `/readyz` endpoint reads the latest background
readiness snapshot and does not query Kafka synchronously.

Post Embedding readiness also tracks uncommitted work by assigned partition
within the active Consumer Group Generation. Pending starts when a valid batch
is fetched, survives processing and commit retries and Reader restarts, and
clears only after a confirmed Offset Commit covers its target. A pending batch
older than two minutes reports `worker_post_embedding_partition_stalled`;
idle or unassigned partitions do not create Pending state. Generation ID and
Session Version checks reject late events from an older Generation.
