# Like persistence v4: Redis state aggregation

The online Like path has one implementation: Redis atomically stores the current User relation and Post aggregate. PostgreSQL `posts.like_count`, `posts.like_sync_version`, and `post_reaction` are asynchronous Kafka projections. A lagging SQL projection is never used to rebuild an ordinary request.

## Redis keys and write path

- `user:likes:{userID}` is a Set of liked Post IDs. Sentinel `0` means the User Set was initialized.
- `post:like:{postID}:ready`, `post:like:{postID}:count`, and `post:like:{postID}:version` hold the Post aggregate state.
- `post:likes:dirty`, `post:likes:processing`, and `post:likes:claims` carry absolute Post snapshot delivery state.
- `post:likes:behavior:dirty`, `post:likes:behavior:state`, `post:likes:behavior:processing`, and `post:likes:behavior:claims` carry the latest User/Post behavior state and delivery claims. A pair is encoded as `userID:postID`.
- `post:likes:registry` tracks initialized Post aggregates; `post:like:{postID}:rebuild-token` fences trusted initialization against deletion.

One Lua mutation checks the User sentinel, Post Ready/Count/Version, deleted fence, and Redis key types before changing anything. A Like or Unlike changes Count and Version only when the relation changes, then marks the existing Snapshot and Behavior queues dirty. No Like request writes `post_reaction` directly.

## Delivery and projections

The existing Snapshot and Behavior relays publish to Kafka. Snapshot consumers apply absolute Post counts with version checks. User behavior consumers apply version-guarded reactions and deduplicate delivered events through the existing inbox. Kafka event schemas and HTTP Like success responses remain unchanged.

## Lifecycle and recovery

New registrations initialize `user:likes:{userID}` to `{0}` inside the SQL registration transaction before tokens are issued. DevData and load-test setup initialize only newly inserted Users. Login, token refresh, and ordinary Like requests do not create or reset a missing User Set.

Trusted new Post creation and DevData imports use a rebuild token, deletion fence, zero-state checks, and at most three attempts for transient infrastructure errors while retaining the same token. Existing Post Count/Version loss fails closed. A SQL value of zero does not prove Kafka has projected every earlier Like.

If new Post initialization still fails, inspect the Post and queues in a quiesced development environment. The maintenance command is dry-run by default:

```powershell
go run ./cmd/recover-post-like-state --post-id=123
```

Mutation requires all explicit confirmations and repeats the SQL/Redis preflight after taking a rebuild token:

```powershell
go run ./cmd/recover-post-like-state `
  --post-id=123 `
  --apply `
  --confirm-development-reset `
  --confirm-like-writes-paused `
  --confirm-snapshot-behavior-kafka-drained
```

The command refuses nonzero SQL Count/Version, any Post reaction rows, partial or previously registered Redis state, active Snapshot claims, expiry/recovery markers, or nonempty global Behavior queues. The operator must separately confirm Kafka lag is drained and Like history is disposable. It never runs `FLUSHDB`, clears queues, or reconstructs User relations. A Post with a valid `ready=1` state is left unchanged.

The one-time pre-launch User initializer is `go run ./cmd/initialize-user-likes`. It requires `--confirm-development-reset` and `--confirm-api-worker-kafka-quiesced`, keyset-pages SQL Users, preserves valid existing Sets, and refuses SQL projection history. It does not clear projections, queues, or Kafka.

Existing User Set or Post aggregate loss has no automatic online recovery. Redis outages, missing User sentinel, missing Post state, type errors, and detectable Count inconsistencies fail closed. Same-ID DevData Post reactivation remains rejected because a User Set stores PostID without a lifecycle generation.

## Deleted relation cleanup

The background worker keyset-pages Users (64 IDs), then scans one User Set with `SSCAN COUNT 128` per pass. Redis `COUNT` is a hint; over-returned members remain pending and each SQL check/Lua removal batch is capped at 128 IDs. Completing the SQL pages resets the cursor for the next sweep. A worker restart begins at the first User again; removals are idempotent.

SQL confirms Post lifecycle before Lua removes a relation. Lua preserves sentinel `0`, active `ready=1` relations, Post Count/Version, Snapshot Dirty, and Behavior events. A malformed User Set is logged and skipped for the current sweep so later Users proceed; transient SQL/Redis failures retain progress and retry. A Post Ready type error or SQL-deleted/Redis-active mismatch protects only that candidate and is reported; the next sweep can reconsider it.

## Expiry

Post aggregate expiry remains disabled while User Sets persist independently. Setting `LIKE_STATE_EXPIRY_ENABLED=true` fails validation with the stable `ErrLikeStateExpiryUnsupported` error. Do not enable Post-only TTL or clear Post Count/Version as an expiry substitute.

SPEC-03 performance work remains separate: optimizing global Snapshot Dirty claims, partitioning global Behavior queues, Redis Cluster cross-slot Lua changes, and high-throughput end-to-end measurements.
