# Kafka Consumer DLQ Runbook

`kafka-dlq` inspects the unified `ConsumerDLQTopic` and can republish a validated original business message to its configured source topic. Replay is at-least-once. The consumer's inbox, version gate, dedupe, or state-reconciliation guard must make duplicate delivery safe.

Run commands from the `Go.exchange` directory with Kafka access configured. The standard migration creates `kafka_dlq_replays`; API and Worker readiness do not depend on this maintenance-only table.

## List

```bash
go run ./cmd/kafka-dlq list --limit 100
```

List output contains safe metadata only. It does not print the source key, value, or headers.

## Filter

```bash
go run ./cmd/kafka-dlq list \
  --consumer user_behavior_projection \
  --error-code unsupported_schema
```

Use `--source-topic`, `--error-class`, `--event-id`, `--failed-after`, and `--failed-before` to narrow results. Time values must be RFC3339. `--limit` caps returned records (default 100, maximum 500). `--scan-limit` caps DLQ Kafka records actually inspected (default 10000, maximum 100000). The command reports `scanned`, `matched`, `returned`, and `truncated`; when the scan cap may have left records unread, it prints `scan limit reached; narrow the filters or increase --scan-limit`.

## Show

```bash
go run ./cmd/kafka-dlq show \
  --partition 1 \
  --offset 4821
```

`show` explicitly displays the selected source key, value, headers, and failure reason. Key, value, and header values are shown as base64 with a safe UTF-8 preview. Treat this output as potentially sensitive.

## Dry run

```bash
go run ./cmd/kafka-dlq replay \
  --partition 1 \
  --offset 4821
```

Replay without `--execute` only reads and validates the record and prints a `ReplayPlan`. It does not publish to Kafka or write an audit row. The same consumer/source-topic policy validation is used for dry run and execution.

## Execute

```bash
go run ./cmd/kafka-dlq replay \
  --partition 1 \
  --offset 4821 \
  --execute
```

Execution inserts an audit row with status `started`, attempts the publish, and records `succeeded` if the publisher returns success. If publishing returns an error, the outcome is `unknown`: a timeout or disconnect cannot prove the broker did not accept the record. A failure while updating the audit after publishing stops the command and must not trigger an automatic second publish.

## Filtered batch dry run

```bash
go run ./cmd/kafka-dlq replay \
  --consumer user_behavior_projection \
  --error-code unsupported_schema \
  --limit 100
```

## Filtered batch execute

```bash
go run ./cmd/kafka-dlq replay \
  --consumer user_behavior_projection \
  --error-code unsupported_schema \
  --limit 100 \
  --execute
```

Batch execution requires `--consumer` plus at least one narrowing selector (`--error-code`, `--event-id`, `--failed-after`, `--failed-before`, or `--source-topic`). Batch size is capped at 500. A batch stops at the first publish or audit error and reports attempted, succeeded, unknown, and remaining counts. Exit code 2 indicates that earlier records succeeded before the batch stopped.

## Safety and audit notes

- Never reset the normal production consumer-group offset to replay an individual DLQ record.
- Replay always targets the source topic stored in the record. There is no arbitrary target-topic option.
- The source key, value, and original non-replay headers are preserved. Kafka's configured partitioner chooses the replay partition; the original partition is audit metadata only. Replay time and provenance headers are new.
- Replay policy pairs are explicit: `like_snapshot_projection` / Like Snapshot uses the inbox plus `like_sync_version`; `user_behavior_projection` and `recommendation_metrics` use `ConsumerInbox` and their projection guards; `notification_projection` uses `ConsumerInbox` plus notification dedupe/source-version checks; `post_embedding` uses current post content and embedding content-hash reconciliation.
- `started` rows older than the normal command duration must be treated as publish outcome unknown. Inspect the source topic and consumer effects before deciding whether to replay again.
- The DLQ topic is provisioned with `retention.ms=2592000000` (30 days). The development DLQ topic can be deleted and recreated once when applying this schema cleanup; do not delete production DLQ data as part of an incident replay.
- `cmd/requeue-post-embeddings` remains the state-reconciliation tool for missing or stale embeddings. It is not DLQ replay.
