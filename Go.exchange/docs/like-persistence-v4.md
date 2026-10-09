# Like persistence v4: Redis state aggregation

The online like path has one implementation only: Redis atomically stores the current user state, Post count, and Post version. PostgreSQL is an asynchronous projection. A SQL projection is not used to bootstrap ordinary requests because Kafka may still be behind; there is no DB direct like write, Redis Stream, dual write, or runtime mode switch.

## Write path

One Lua script validates the Redis baseline, applies SADD or SREM for the user, changes count/version only on state change, and writes:

- article:likes:dirty for the absolute article snapshot;
- article:likes:behavior:state for the latest user/article state;
- article:likes:behavior:dirty for behavior delivery.

The encoded behavior state is liked|version|occurred_at and the pair key is user_id:article_id.

## Delivery

A timed dispatcher runs every second. It claims at most 500 dirty pairs, loads their latest state, publishes one Kafka batch, then ACKs only if pair, claim_id, and emitted version still match.

A newer state during publishing remains dirty. An expired lease is reclaimed safely. Kafka event IDs are stable: like-state:{user_id}:{article_id}:{version}; the Kafka key is user_id:article_id.

The one-second window bounds behavior output by active pairs rather than request count. API reads and writes remain synchronous against Redis.

## Projection

Kafka consumers apply absolute article snapshots only when the version is newer. User reactions and article behavior rows are also version guarded. consumer_inboxes deduplicates repeated Kafka delivery.

## Configuration

| Variable | Default |
| --- | --- |
| LIKE_SNAPSHOT_POLL_INTERVAL | 1s |
| LIKE_SNAPSHOT_BATCH_SIZE | 100 |
| LIKE_CLAIM_LEASE | 30s |
| LIKE_BEHAVIOR_BATCH_SIZE | 500 |
| LIKE_BEHAVIOR_CLAIM_LEASE | 30s |
| LIKE_BEHAVIOR_FLUSH_INTERVAL | 1s |
| LIKE_BEHAVIOR_PROJECTION_CONSUMERS | 6 |

## Recovery and backfill

Backfill is an explicit quiesced operation. Pause API writes, wait for snapshot and behavior queues plus Kafka lag to reach zero, verify article count equals active reactions, then run cmd/backfill-likes with LIKE_BACKFILL_QUIESCED=true.

The online implementation has no fallback to the former DB or Stream paths. Redis unavailability or a missing User/Post baseline therefore returns an error rather than silently changing consistency semantics. `ErrUserLikeNotReady` and `ErrPostLikeNotReady` remain compatible with `errors.Is(err, likes.ErrNotReady)` but are separately logged and measured.

## User and Post lifecycle

New registrations initialize `user:likes:{userID}` with sentinel `0` inside a
controlled SQL registration transaction, before token issuance. DevData and
load-test provisioning initialize only newly inserted User rows. Login,
refresh, and ordinary Like requests never repair existing User Sets.

Trusted new Post creation retains rebuild-token and deletion-fence protected
zero-state initialization. A missing existing Post aggregate fails closed; a
SQL value of zero does not prove Kafka has projected every prior mutation.
Same-ID DevData Post reactivation is rejected before SQL changes because stale
User membership has no lifecycle version.

Deleted Posts keep the existing durable `PostLikeCleanup` and Redis deletion
fence. A separate worker keyset-pages SQL Users, scans User Sets with
`SSCAN COUNT 128`, confirms candidate Post lifecycle in SQL, and conditionally
removes only missing/soft-deleted Post relations. Each SQL/Lua batch is capped
at 128 IDs; sentinel `0`, active Redis `ready=1` relations, Post Count/Version,
Snapshot Dirty, and Behavior events are preserved. See
[`like-state-recovery.md`](like-state-recovery.md) for the one-time
development initializer and incident procedure.
