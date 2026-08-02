# Recommendation telemetry V1

Recommendation telemetry records viewable impressions and first clicks without changing `ArticleBehavior` or recommendation weights.

## Runtime contract

- `GET /api/recommendations/articles` keeps its array response and adds optional per-item `tracking` metadata.
- `POST /api/recommendation-events` accepts at most 50 signed events and returns per-event `accepted`, `duplicate`, or `rejected` results.
- PostgreSQL `recommendation_events` is the source of truth.
- One accepted HTTP batch produces at most one `recommendation.events.recorded` Outbox event.
- Kafka topic `goexchange.recommendation.events.v1` feeds the rebuildable `recommendation_daily_metrics` projection.
- Delivery is at least once. `consumer_inboxes` and database uniqueness prevent double counting.

## Enablement

Telemetry is disabled by default. Configure the API service with:

```text
RECOMMENDATION_TELEMETRY_ENABLED=true
RECOMMENDATION_TELEMETRY_ROLLOUT_PERCENT=100
RECOMMENDATION_TELEMETRY_SIGNING_KEY=<at least 32 random bytes>
RECOMMENDATION_TELEMETRY_TOKEN_TTL=24h
RECOMMENDATION_TELEMETRY_MAX_CLOCK_SKEW=5m
RECOMMENDATION_TELEMETRY_EVENTS_PER_MINUTE=1000
```

The signing key must be identical across API replicas and must not be committed. During rollback, set rollout percent to `0` first and leave ingestion and the worker running for at least one token TTL.

## CTR query

Aggregate counts before division:

```sql
SELECT
  metric_date,
  scene,
  ranker_version,
  ranker_config_hash,
  strategy_id,
  position,
  SUM(click_count)::numeric / NULLIF(SUM(impression_count), 0) AS ctr
FROM recommendation_daily_metrics
GROUP BY metric_date, scene, ranker_version, ranker_config_hash, strategy_id, position;
```

## Rebuild

Rebuild a UTC date range from raw facts:

```powershell
go run ./cmd/rebuild-recommendation-metrics --from 2026-07-01 --to 2026-07-30
```

Raw events should be retained for 90 days. Do not use canary-period counts as full traffic totals while rollout percent is below 100.
