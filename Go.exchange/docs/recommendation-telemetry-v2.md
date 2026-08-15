# Recommendation Telemetry v2

The recommendation API serves deterministic rules_v3. Telemetry is an attribution and measurement protocol; affinity is derived server-side from the durable facts.

## Contract

Each successful GET /api/recommendations/articles creates one UUID request ID. The same ID is embedded in every signed card token and recorded in recommendation_requests. Request persistence is best effort and does not change a successful recommendation response.

POST /api/recommendation-events accepts impression, click, read_end, and not_interested. The v2 tracking token binds the authenticated user, article, request, position, ranker, strategy, token lifetime, estimated_read_time_ms, and read_policy_version. The client cannot choose those values.

A read_end payload contains only foreground_time_ms, scroll_progress_percent, and exit_type. The server derives read_outcome under read_v1:

- minimum dwell is clamp(estimated_read_time * 0.35, 3s, 20s);
- strong dwell is clamp(estimated_read_time * 0.80, 3s, 45s);
- qualified wins when strong dwell is reached, or when minimum dwell and progress of at least 50% are both reached;
- otherwise quick_bounce requires foreground time below 3 seconds and progress below 10%; all remaining reads are neutral.

The server rejects malformed ranges, invalid token-bound context, and read fields on non-read events. Estimate and outcome are never accepted from the client.

## Browser measurement

The detail page measures foreground time with a monotonic performance clock. Visibility changes pause and resume the foreground clock; hiding the tab does not finish the session. Initial viewport visibility is not progress. Progress advances only on actual scroll events through the initially unread region, is monotonic, and is not advanced by resize or reflow. Short posts therefore report zero progress. Route leave and pagehide finish the session once; finish is idempotent.

Recommendation cards emit one impression after one second at 50% visibility. Click attribution is session-scoped, and the detail page consumes only the matching article attribution. Events are buffered in session storage under the v2 queue key and retried with event and business idempotency.

## Delivery and metrics

Accepted facts are written with an outbox event, then projected through Kafka and ConsumerInbox into recommendation_daily_metrics. Metrics use the server-derived qualified/quick-bounce outcomes and remain rebuildable from durable facts. Non-read events must not carry read-only fields.

## Local configuration

The root D:\code\mf\docker-compose.yml supplies the recommendation signing key through RECOMMENDATION_TELEMETRY_SIGNING_KEY. AI_API_KEY belongs only to the article AI-processing worker. Keep signing keys outside source control and revoke or rotate any provider credential that was previously exposed in a committed configuration outside the repository. Token TTL and rate limits remain server configuration; read classification is fixed by read_v1.