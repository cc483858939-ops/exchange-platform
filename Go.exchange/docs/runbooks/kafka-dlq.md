# Kafka Consumer DLQ Runbook

`kafka-dlq` inspects the unified `ConsumerDLQTopic` and can republish a validated original business message to its configured source topic. Replay is at-least-once. The consumer's inbox, version gate, dedupe, or state-reconciliation guard must make duplicate delivery safe.

Run the production CLI from the repository root using the maintenance profile. The standard migration creates `kafka_dlq_replays`; API and Worker readiness do not depend on this maintenance-only table.

## List

```bash
docker compose --env-file deploy/.env -f deploy/compose.prod.yml \
  --profile maintenance run --rm kafka-dlq list --limit 100
```

List output contains safe metadata only. It does not print the source key, value, or headers.

## Filter

```bash
docker compose --env-file deploy/.env -f deploy/compose.prod.yml \
  --profile maintenance run --rm kafka-dlq list \
  --consumer user_behavior_projection \
  --error-code unsupported_schema
```

Use `--source-topic`, `--error-class`, `--event-id`, `--failed-after`, and `--failed-before` to narrow results. Time values must be RFC3339. `--limit` caps returned records (default 100, maximum 500). `--scan-limit` caps DLQ Kafka records actually inspected (default 10000, maximum 100000). Output separates `scan_truncated` from `match_truncated`, counts malformed records, and marks partial selections `executable=false`. Invalid samples contain only partition, offset, and a safe error category.

## Show

```bash
docker compose --env-file deploy/.env -f deploy/compose.prod.yml \
  --profile maintenance run --rm kafka-dlq show \
  --partition 1 \
  --offset 4821
```

By default, `show` prints metadata, source key/value sizes, the header count, and header key names. Add `--include-payload` only when needed:

```bash
docker compose --env-file deploy/.env -f deploy/compose.prod.yml \
  --profile maintenance run --rm kafka-dlq show \
  --partition 1 --offset 4821 --include-payload
```

`--include-payload` prints key, value, and header values as base64 with safe UTF-8 previews. The output may contain sensitive business content; do not write it to CI or incident logs casually.

## Dry run

```bash
docker compose --env-file deploy/.env -f deploy/compose.prod.yml \
  --profile maintenance run --rm kafka-dlq replay \
  --partition 1 \
  --offset 4821
```

Replay without `--execute` only reads and validates the record and prints a `ReplayPlan`. It does not publish to Kafka or write an audit row. The same consumer/source-topic policy validation is used for dry run and execution.

## Execute

```bash
docker compose --env-file deploy/.env -f deploy/compose.prod.yml \
  --profile maintenance run --rm kafka-dlq replay \
  --partition 1 \
  --offset 4821 \
  --execute
```

Execution inserts an audit row with status `started`, attempts the publish, and records `succeeded` if the publisher returns success. If publishing returns an error, the outcome is `unknown`: a timeout or disconnect cannot prove the broker did not accept the record. A failure while updating the audit after publishing stops the command and must not trigger an automatic second publish.

## Filtered batch dry run

```bash
docker compose --env-file deploy/.env -f deploy/compose.prod.yml \
  --profile maintenance run --rm kafka-dlq replay \
  --consumer user_behavior_projection \
  --error-code unsupported_schema \
  --limit 100
```

## Filtered batch execute

```bash
docker compose --env-file deploy/.env -f deploy/compose.prod.yml \
  --profile maintenance run --rm kafka-dlq replay \
  --consumer user_behavior_projection \
  --error-code unsupported_schema \
  --limit 100 \
  --execute \
  --confirm-plan "$PLAN_HASH"
```

Set `PLAN_HASH` to the `plan_hash` printed by the matching dry-run.

Batch execution requires `--consumer`, a strong selector (`--error-code`, `--event-id`, `--failed-after`, or `--failed-before`), and `--confirm-plan` from a dry-run with the same options. `--source-topic` and `--error-class` are auxiliary filters and do not satisfy this requirement. Records are ordered by DLQ partition and offset ascending. The CLI rescans and rehashes before opening the audit database or publisher; a mismatch prints `replay plan changed; run dry-run again`. Scan truncation, match truncation, or malformed records disable execution. Batch size is capped at 500. A batch stops at the first publish or audit error and reports attempted, succeeded, unknown, and remaining counts. Exit code 2 indicates that earlier records succeeded before the batch stopped.

The plan hash uses a length-framed canonical representation of plan version, DLQ topic, normalized selectors, and each ordered candidate's DLQ/source location, consumer, event ID, error code, and UTC `failed_at`. Its source-message digest covers key, value, and ordered header key/value bytes, preserving nil-versus-empty byte slices.

## Safety and audit notes

- Never reset the normal production consumer-group offset to replay an individual DLQ record.
- Replay always targets the source topic stored in the record. There is no arbitrary target-topic option.
- The source key, value, and original non-replay headers are preserved. Kafka's configured partitioner chooses the replay partition; the original partition is audit metadata only. Replay time and provenance headers are new.
- Replay policy pairs are explicit: `like_snapshot_projection` uses the versioned like-snapshot gate; `user_behavior_projection` uses `ConsumerInbox` plus the versioned post reaction gate; `recommendation_metrics` uses `ConsumerInbox` and projection guards; `notification_projection` uses `ConsumerInbox` plus notification dedupe/source-version checks; `post_embedding` uses current post content and embedding content-hash reconciliation.
- `started` rows older than the normal command duration must be treated as publish outcome unknown. Inspect the source topic and consumer effects before deciding whether to replay again.
- The DLQ topic is provisioned with `cleanup.policy=delete` and `retention.ms=2592000000` (30 days). The development DLQ topic can be deleted and recreated once when applying this schema cleanup; do not delete production DLQ data as part of an incident replay.
- The maintenance service receives the isolated `KAFKA_DLQ_DATABASE_DSN`; API and Worker do not. List, show, and dry-run do not open PostgreSQL. Execute requires the DSN and fails before publish when it is missing. Grant the audit role only `CONNECT`, schema `USAGE`, and `SELECT`, `INSERT`, and `UPDATE` on `kafka_dlq_replays`, plus `USAGE` on its sequence if required by the schema.
- `cmd/requeue-post-embeddings` remains the state-reconciliation tool for missing or stale embeddings. It is not DLQ replay.
