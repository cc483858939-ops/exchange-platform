# Like deletion tombstone reclamation

## Protocol

All state creation requires a random, expiring `post:like:<id>:rebuild-token`
acquired **before** loading the current active SQL Post and reaction baseline.
Initialize/Recover check that token in the same Lua execution as the writes.
Missing, expired, replaced or revoked tokens fail closed. Existing registry,
projection-completeness and recoverable-version fences still apply to recovery.
Request timeouts are not the correctness boundary.

Deletion atomically writes a persistent `ready=deleted` fence and revokes the
token, independently of live-state/index type faults. Purge also revokes tokens.
Only a successful purge can arm the tombstone TTL. Failed cleanup retains the
fence and the existing durable SQL retry work. Successful operations release
their own token conditionally; abandoned tokens expire automatically.
Duplicate deletion attempts preserve a TTL already armed by successful cleanup.

Same-ID DevData reactivation is rejected before SQL clears `deleted_at` or
changes the mirror mapping. The User -> Posts relation has no lifecycle version,
so stale memberships cannot be distinguished from a new Like in a reused Post
identity. A rebuild token cannot solve that relation ambiguity. Regular request
paths also fail closed on missing Post or User state and never bootstrap it.

## Configuration

Deletion tombstone expiry is enabled by default; no environment setting is
required. `LIKE_DELETION_TOMBSTONE_TTL` defaults to `24h` and
`LIKE_REBUILD_TOKEN_TTL` defaults to `30s`. Slow rebuilds exceeding their token
lease are rejected and must start again with a fresh SQL read. Ordinary hot
reads and Like mutations do not acquire tokens or query SQL. Trusted new Post
creation and DevData import retry transient infrastructure failures at most
three times while retaining the same token and deleted fence. If initialization
still fails, the dry-run-first `cmd/recover-post-like-state` command provides a
quiesced development-only recovery path; its confirmation and refusal rules are
documented in `docs/like-state-recovery.md`.

Setting `LIKE_DELETION_TOMBSTONE_EXPIRY_ENABLED=false` explicitly keeps future
deletion fences persistent. It does not remove TTLs already armed. Normal
maintenance uses its existing indexes and does not perform a keyspace scan.
Redis restart/eviction loses tokens too, so old operations fail closed.

## User relation cleanup

`tasks.startUserLikeRelationCleanup` pages PostgreSQL User IDs by ascending
keyset pages of 64 and issues one `SSCAN COUNT 128` command per pass for the
current `user:likes:{id}` Set. Redis treats COUNT as a scan hint; any
over-returned members are retained and consumed in later bounded batches. When
the SQL query after the maximum UserID returns an empty page, the worker resets
the cursor and starts a new sweep. The worker verifies at most 128 candidate
Post IDs per SQL query and runs a conditional Lua remove batch
that deletes only SQL-confirmed soft-deleted or missing Post IDs whose Redis
Ready fence is still absent or `deleted`. A `ready=1` fence is preserved. The
script never removes sentinel `0`, changes Post Count/Version, or emits Like
events. User Set type errors and missing sentinels skip only that User for the
current sweep; a later sweep retries it. Wrong-type Post Ready keys and
lifecycle mismatches protect only the affected candidate and do not block other
candidates. Infrastructure errors retain the current User and pending members
for retry. The cursor and candidate page stay in worker memory; after a restart
the sweep resumes from the first SQL User ID and safely repeats prior work.

## Stock User initialization

The one-time command is `go run ./cmd/initialize-user-likes` and requires both
`--confirm-development-reset` and `--confirm-api-worker-kafka-quiesced`. Before
running it, stop API and Like workers, review Redis dirty/processing queues and
Kafka lag, and confirm there is no Like history to retain. The command refuses
nonzero SQL Like Count/Version or existing Like reaction rows. It reads User
IDs by bounded SQL keyset pages, never runs Redis `KEYS` or `SMEMBERS`, and only
adds sentinel `0` to an absent/empty Set. It preserves an initialized Set and
rejects nonempty Sets missing the sentinel. It does not clear Redis queues,
Kafka events, reactions, or Post projections; any development reset of those
systems must be a separate, explicit, reviewed operation.

## Verification

Use disposable Redis/PostgreSQL instances with `REDIS_TEST_ADDR`,
`REDIS_TEST_DB` and `POSTGRES_TEST_DSN`. The focused integration cases cover stale
zero-bootstrap/initialization after tombstone expiry, independent token expiry,
conditional release, deletion during an outstanding rebuild, persistent fences
on cleanup failure, successful retry, and fail-closed DevData reactivation
before SQL mutation. User relation cleanup integration tests verify SQL
lifecycle checks, unexpired and expired tombstones, multi-round pagination,
corrupt-user isolation, infrastructure retries, and sentinel preservation.
Without those settings, the corresponding Redis/PostgreSQL tests explicitly
skip.
