# Like state lifecycle and recovery runbook

This runbook describes the SPEC-02 Redis `User -> Posts` relation lifecycle.
The request path stays Redis-first. PostgreSQL Like Count, Version, and
`post_reaction` are asynchronous projections and are not a safe automatic
bootstrap source while Kafka may still be behind.

## Runtime behavior

| Condition | Runtime behavior |
| --- | --- |
| New registration | Create the SQL User in a transaction, initialize Redis Set `{0}`, commit SQL, then issue tokens. Redis failure rolls back SQL and returns 503. |
| Existing User Set missing or missing sentinel | Fail closed. Login and refresh do not initialize it. |
| New Post | The trusted create path initializes Count/Version/Ready through the existing rebuild token and deleted fence. It retries transient infrastructure failures at most three times with the same token. A failure is logged and measured; the Post is not Ready in Redis. |
| Existing Post Count/Version/Ready missing | Fail closed with Post NotReady. HTTP requests do not load a SQL baseline or write zero. |
| Post deleted | Redis deletion fence rejects Like/Unlike; durable `PostLikeCleanup` retries propagation. |
| Wrong Redis key type or inconsistent Count | Return an explicit unavailable error and emit a bounded lifecycle metric. Do not repair automatically. |
| DevData same-ID Post reactivation | Reject before SQL clears `deleted_at` or updates the mirror mapping. |

The public Like response contract and Kafka event schema are unchanged. Successful
mutations still update Redis relation, Count, Version, Snapshot Dirty, and
Behavior Dirty in the existing Lua script. Existing snapshot/behavior relays,
version checks, and consumer deduplication remain responsible for PostgreSQL
projection.

## User Like Set TTL and cold restore

User-to-Post Sets can use an independent sliding TTL. The default is 72 hours,
controlled by `USER_LIKE_SET_TTL`; only `user:likes:{uid}` expires. The Post
`ready`, `count`, and `version` keys remain persistent. Post deletion tombstone
expiry continues to use its existing lifecycle.

The switches are independent:

| Setting | Default | Purpose |
| --- | --- | --- |
| `USER_LIKE_TTL_ARMING_ENABLED` | `false` | Arm or refresh User Set TTLs on initialization and Like/Unlike mutations. |
| `USER_LIKE_TTL_RESTORE_ENABLED` | `true` | Permit restore of a verified cold User Set. |
| `USER_LIKE_SET_TTL` | `72h` | Active User Set lifetime; accepted production configuration is 1 hour through 365 days. |
| `USER_LIKE_RESTORE_LOCK_TTL` | `30s` | Redis restore ownership duration. It must exceed the request timeout by at least one second. |
| `USER_LIKE_RESTORE_BATCH_SIZE` | `500` | Keyset page and Redis SADD batch size, bounded to 5000. |
| `USER_LIKE_RESTORE_MAX_RELATIONS` | `100000` | Per-user recovery cap, bounded to 1,000,000. |
| `USER_LIKE_RESTORE_REQUEST_TIMEOUT` | `2s` | Total SQL/Redis restore budget, bounded to 30 seconds. |
| `USER_LIKE_RESTORE_CONCURRENCY` | `8` | Per-process restore limit, bounded to 64; Redis lock also coordinates API instances. |

When arming is enabled, initialization and every successful Like or Unlike,
including idempotent requests, update the Set expiration and
`user:likes:expiry:ledger` field in the same Lua invocation. Redis server
`TIME` supplies the deadline. The ledger is a persistent shared HASH keyed by
decimal UserID; it is not a relation snapshot. Read requests do not renew the
TTL. With arming disabled, a successful mutation persists the active Set and
removes its ledger field. Post aggregate keys are never passed as TTL targets.

A missing User Set is cold only when its ledger field contains a valid deadline
and Redis server time has reached that deadline. Missing/invalid ledger data,
an early missing key, a wrong Redis type, or a Set without sentinel `0` fails
closed. No path initializes an empty Set based only on `EXISTS == 0`.

On a verified cold request, one API instance obtains a token-valued Redis lock.
The worker reads an active SQL User and then `post_reaction` in a
`REPEATABLE READ READ ONLY` transaction, using ascending `post_id` keyset pages.
It includes `liked=true` rows for the Like reaction and joins active Posts only
to exclude deleted or physically missing Posts. It deliberately does not filter
visibility, so a private but non-deleted Post retains its relation. Each page
is added to a token-specific temporary Set containing `0`; the temporary key
and its TTL are created atomically. A final Lua script checks the ledger, lock
token, missing formal key, temporary sentinel, exact relation cardinality, and
configured cap before checking each recovered Post's Redis deletion fence and
atomically renaming the complete Set into place. This bounded final check closes
the race where a Post was deleted after the repeatable-read SQL snapshot. A
stale lock owner cannot replace a newer result. Failure removes only that
owner's temporary key and lock; the formal Set remains absent. Restoring a
relation Set never changes a Post Count/Version and does not emit Like events.
The next original request is retried once after restore.

The 72-hour interval is an operating assumption that normal Kafka projection
lag fits within the retention window. It is not a proof against arbitrarily long
Kafka outage, lost messages, or corrupted PostgreSQL projection. Monitor Kafka
consumer health/lag and restore outcomes. During a prolonged projection outage,
pause rollout/arming, retain cold ledgers and Sets where possible, and use the
existing Kafka recovery and data repair procedures before relying on cold-user
recovery. This feature adds no per-user Kafka offset barrier or second relation
snapshot table.

### Enable and migrate existing Sets

The migration command reads SQL UserIDs in ascending keyset pages and changes
one Redis User Set at a time. It defaults to dry-run, reports a resume UserID,
and never scans every Redis key or flushes a database. Its Lua migration keeps
the later existing deadline when a Like mutation races the migration. A cold
User Set is preserved; unexpected missing/malformed state is reported as an
anomaly for review.

1. Deploy the code with arming off and restore on. Confirm the Redis server is
   the configured Redis 7 production image and that `post_reaction` projection
   health is acceptable.
2. Run the dry-run and inspect anomalies and counts:

   ```powershell
   go run ./cmd/migrate-user-like-ttl --mode=arm --page-size=200
   ```

3. Set `USER_LIKE_TTL_ARMING_ENABLED=true` consistently on every API instance.
   Apply the keyset migration with the explicit confirmation:

   ```powershell
   go run ./cmd/migrate-user-like-ttl `
     --mode=arm --apply --page-size=200 `
     --confirm-all-api-instances-arming-enabled
   ```

4. Review the completion summary and metrics. If interrupted, resume with the
   reported `--start-after-user-id`; repeating an earlier page is idempotent.

An orphan-ledger check is deliberately per UserID, not a background global
scan. For review, run `--mode=audit-orphan --user-id=<id>`; to remove a field,
pause registration and add `--apply --confirm-user-registration-paused`. The
command checks that the SQL User row (including soft-deleted rows) and Redis
User Set are both absent, then a Lua compare-and-delete protects against a
changed ledger field.

### Pause or roll back TTL arming

Setting arming false stops new TTLs but does not itself cancel deadlines already
written. First run the new code everywhere with arming disabled; successful
mutations persist active Sets and remove their ledger fields, while a cold Set
still restores if restore remains enabled. Stop all old arming processes, then
run the rollback dry-run:

```powershell
go run ./cmd/migrate-user-like-ttl --mode=rollback --page-size=200
```

After reviewing the results, apply with the switch disabled and both explicit
process confirmations:

```powershell
go run ./cmd/migrate-user-like-ttl `
  --mode=rollback --apply --page-size=200 `
  --confirm-old-arming-processes-stopped `
  --confirm-all-api-instances-arming-disabled
```

The rollback applies `PERSIST` only to an existing valid Set and removes that
User's ledger field. A verified cold User remains cold with its ledger. If that
User is restored while arming is off, the complete restored Set is persistent
and its ledger field is removed. Keep restore enabled until all cold Users have
recovered or have been separately handled. Do not restart any old arming
processes during or after rollback.

### Validation

Unit tests run without external services. Redis lifecycle integration tests use
`REDIS_TEST_ADDR` and optional `REDIS_TEST_DB`; PostgreSQL/Redis restore tests
also require `POSTGRES_TEST_DSN`. They use unique IDs and delete only exact test
keys/rows. The real Kafka end-to-end test additionally requires
`KAFKA_BROKERS`; a skipped test is not evidence of Kafka projection success.

The opt-in memory/latency benchmark uses a separate `REDIS_BENCH_ADDR` and
`POSTGRES_BENCH_DSN`. It defaults to dry-run, requires a nonzero empty Redis DB
and a PostgreSQL database name containing `bench`, `test`, `integration`, or
`disposable`, and never runs `FLUSHDB`. It creates the required 0/10/100/1000/
10000 relation cohorts for persistent baseline, active Ledger, expired cold,
and restored states, then removes only its generated Redis keys and drops only
its uniquely named PostgreSQL schema. It reports Redis total `used_memory` and
the change attributable to each newly populated scenario, User Set
`MEMORY USAGE`, ledger growth, net cold memory change, hot Like/Unlike p95/p99,
restore latency, projected row count, and the measured plan for the keyset
query. The installed Set object footprint is the post-rename snapshot of the
temporary Set; the concurrent `used_memory` sampling is the Redis-wide peak
sample and can miss a brief allocator high-water mark. The benchmark uses a
short injected expiry to avoid waiting 72 hours, so those cold-memory results
must not be presented as 72-hour production measurements.

```powershell
go run ./cmd/benchmark-user-like-ttl

# Only against dedicated disposable services with REDIS_BENCH_ADDR and
# POSTGRES_BENCH_DSN configured:
go run ./cmd/benchmark-user-like-ttl `
  --apply --confirm-disposable-services --db=15 --expire-after=2s
```

The production Compose file pins Redis 7, whose Lua script replication mode
supports the `TIME`-based atomic expiry/ledger update used here. Confirm any
non-Compose Redis deployment is Redis 5 or newer, where effect-based script
replication is the default.

## Deleted relation cleanup

The worker scans SQL Users by ascending keyset pages of 64, then uses one
`SSCAN COUNT 128` per pass for the current User Set. COUNT is a Redis hint;
over-returned members remain pending for later passes, while each SQL check and
Lua deletion batch is capped at 128 IDs. Completing the last SQL page resets the
cursor for the next sweep. A SQL error is returned as a retryable pass failure
and does not reset the cursor. The SQL query treats soft-deleted and physically
missing Posts as deleted. Lua removes a relation only while its Ready key is
absent or `deleted`, preserves Ready `1`, and never removes sentinel `0` or
updates any Post aggregate/event key.

A User key type error or missing sentinel is logged and skipped for the current
sweep, so later Users are processed; a later sweep retries that User. A
wrong-type Post Ready key or SQL-deleted/Redis-active mismatch protects only
that candidate, records a bounded lifecycle event, and allows other candidates
to proceed. Redis connection failures retain the current User and pending
members for retry. The counter `go_exchange_user_like_relations_removed_total`
increments by the actual number removed.

Progress is in memory. A worker restart starts from the first SQL User ID and
repeats the idempotent scan. The current PostID-only relation format cannot
distinguish an old Like from a new Like after same-ID Post reactivation; that
transition remains disabled, which makes the SQL-check/conditional-SREM race
safe under this lifecycle.

## One-time initialization before launch

Use this only for an unlaunched development environment where Like history is
disposable. The command adds sentinel `0` to absent or empty User Sets and leaves
valid existing Sets unchanged. It refuses nonempty Sets that lack the sentinel.
It does not reset projections, queues, Kafka, or Redis.

1. Stop API processes and Like workers so there are no active writes or cleanup
   passes.
2. Review Redis Snapshot and Behavior dirty/processing queues and Kafka consumer
   lag. Drain or separately handle any pending events; do not let old events
   replay after a reset.
3. Confirm that current Like data can be discarded. The command independently
   refuses nonzero SQL Post Like Count/Version and existing Like reaction rows.
4. Run with both explicit confirmations and a bounded page size:

   ```powershell
   go run ./cmd/initialize-user-likes `
     --confirm-development-reset `
     --confirm-api-worker-kafka-quiesced `
     --page-size=200
   ```

5. Record the final counts and `last_user_id`. If some User Sets failed, rerun
   from UserID zero so failures and partial progress are retried idempotently.
6. Verify representative User Sets contain `0`, create/initialize a disposable
   Post through the application path, and verify one Like/Unlike updates the
   existing Redis and projection pipeline.
7. Restart API and workers only after the external queue/Kafka review is
   complete.

The tool never runs automatically in API or Worker startup. It does not issue
`KEYS`, `SMEMBERS`, or `FLUSHDB`. If SQL projections or queued events contain
history, stop and use a separately approved development reset or restore
procedure; do not initialize empty User Sets over that state.

## Failed new Post initialization

After the trusted creation path exhausts its bounded transient retries, an
operator may inspect a specific active Post with the recovery command. It is
dry-run by default:

```powershell
go run ./cmd/recover-post-like-state --post-id=123
```

For an apply, stop API and Like workers, review and drain Snapshot/Behavior
queues and Kafka lag, confirm this is an explicitly approved development
recovery with disposable Like history, then pass all confirmations:

```powershell
go run ./cmd/recover-post-like-state `
  --post-id=123 `
  --apply `
  --confirm-development-reset `
  --confirm-like-writes-paused `
  --confirm-snapshot-behavior-kafka-drained
```

The command checks that the Post is active, SQL Count/Version are zero, no
`post_reaction` rows exist, Redis aggregate/registry/token/expiry state is
consistent with a never-initialized Post, Snapshot claims are clear, and global
Behavior queues are empty. It repeats SQL and Redis preflight after taking the
rebuild token. Any evidence of history, partial Redis state, deletion, or queue
work is a refusal; this is a controlled development initialization, not an
online recovery of lost Like history. The command never clears queues or uses
`FLUSHDB`.

## Runtime incident with missing Redis state

1. Keep Like writes closed while investigating. Do not let a normal HTTP
   request rebuild a missing key.
2. Check Redis availability, key types, the User sentinel, Post Ready/Count/
   Version, the deletion fence, and lifecycle logs/metrics.
3. Inspect Snapshot/Behavior queue depth and Kafka lag before treating SQL as a
   baseline. PostgreSQL projections can lag Redis writes and cannot reconstruct
   every User relation when state is missing.
4. If an authoritative Redis backup is available, restore it through the
   approved operational process and verify User relations, Post Count/Version,
   and the existing deletion fences together.
5. If no complete baseline exists, keep affected state unavailable and record
   the affected User/Post range. Any development data reset must be explicit,
   separately reviewed, and coordinated with queues, Kafka, SQL projections,
   and the stock-user initializer.
6. Reopen writes only after representative reads, Like/Unlike mutations,
   snapshot relay, behavior relay, and PostgreSQL projection checks succeed.

## Known limits

- There is no automatic online recovery for an existing User Set or Post
  aggregate lost from Redis.
- Post creation initialization failures receive three bounded same-token
  attempts. There is no durable retry queue; an operator must resolve the
  Redis issue and invoke the explicit Post recovery command in a quiesced
  development environment.
- DevData same-ID reactivation remains unsupported even for apparently zero
  aggregates, because this schema has no proof that stale User Set membership
  never existed.
- Snapshot Dirty full-set claim cost and global Behavior queue scaling remain
  SPEC-03 work. SPEC-02 does not claim end-to-end or high-QPS acceptance.
