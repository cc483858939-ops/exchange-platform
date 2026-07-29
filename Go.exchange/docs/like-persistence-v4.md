# Like persistence v4: Redis state aggregation

The online like path has one implementation only: Redis atomically stores the current user state, article count, and article version. PostgreSQL is an asynchronous projection and Redis baseline source; there is no DB direct like write, Redis Stream, dual write, or runtime mode switch.

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

The online implementation has no fallback to the former DB or Stream paths. Redis unavailability or a missing baseline therefore returns an error rather than silently changing consistency semantics.