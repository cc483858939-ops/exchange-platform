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

Explicit DevData reactivation can acquire a reactivation token while a deletion
fence exists, but must reload the active SQL baseline after acquiring it. A new
deletion revokes this token too. Saved transaction-time snapshots cannot be used
for post-commit initialization. Regular recovery cannot bypass deletion fences.

## Configuration

Deletion tombstone expiry is enabled by default; no environment setting is
required. `LIKE_DELETION_TOMBSTONE_TTL` defaults to `24h` and
`LIKE_REBUILD_TOKEN_TTL` defaults to `30s`. Slow rebuilds exceeding their token
lease are rejected and must start again with a fresh SQL read. Ordinary hot
reads and Like mutations do not acquire tokens or query SQL.

Setting `LIKE_DELETION_TOMBSTONE_EXPIRY_ENABLED=false` explicitly keeps future
deletion fences persistent. It does not remove TTLs already armed. Normal
maintenance uses its existing indexes and does not perform a keyspace scan.
Redis restart/eviction loses tokens too, so old operations fail closed.

## Verification

Use disposable Redis/PostgreSQL instances with `REDIS_TEST_ADDR`,
`REDIS_TEST_DB` and `POSTGRES_TEST_DSN`. The focused integration cases cover stale
zero-bootstrap/initialization after tombstone expiry, independent token expiry,
conditional release, deletion during reactivation, persistent fences on cleanup
failure, successful retry and current DevData SQL reload. Without those settings,
the corresponding tests explicitly skip.
