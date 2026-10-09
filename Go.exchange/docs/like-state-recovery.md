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
| New Post | The trusted create path initializes Count/Version/Ready through the existing rebuild token and deleted fence. A failure is logged and measured; the Post is not reported Ready in Redis. |
| Existing Post Count/Version/Ready missing | Fail closed with Post NotReady. HTTP requests do not load a SQL baseline or write zero. |
| Post deleted | Redis deletion fence rejects Like/Unlike; durable `PostLikeCleanup` retries propagation. |
| Wrong Redis key type or inconsistent Count | Return an explicit unavailable error and emit a bounded lifecycle metric. Do not repair automatically. |
| DevData same-ID Post reactivation | Reject before SQL clears `deleted_at` or updates the mirror mapping. |

The public Like response contract and Kafka event schema are unchanged. Successful
mutations still update Redis relation, Count, Version, Snapshot Dirty, and
Behavior Dirty in the existing Lua script. Existing snapshot/behavior relays,
version checks, and consumer deduplication remain responsible for PostgreSQL
projection.

## Deleted relation cleanup

The worker scans SQL Users by ascending keyset pages, then uses one
`SSCAN COUNT 128` per pass for the current User Set. COUNT is a Redis hint;
over-returned members remain in memory for later passes. Each pass checks at
most 128 Post IDs against SQL and sends at most 128 IDs to a Lua script. The SQL
query treats soft-deleted and physically missing Posts as deleted. Lua removes a
relation only while its Ready key is absent or `deleted`, preserves Ready `1`,
and never removes sentinel `0` or updates any Post aggregate/event key.
The counter `go_exchange_user_like_relations_removed_total` increments by the
actual number removed, and cleanup failures/retries use bounded event labels.

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
- Post creation initialization failures are observable but have no separate
  durable retry queue in this stage; an operator must resolve the Redis issue
  and use a trusted creation/recovery workflow.
- DevData same-ID reactivation remains unsupported even for apparently zero
  aggregates, because this schema has no proof that stale User Set membership
  never existed.
- Snapshot Dirty full-set claim cost and global Behavior queue scaling remain
  SPEC-03 work. SPEC-02 does not claim end-to-end or high-QPS acceptance.
