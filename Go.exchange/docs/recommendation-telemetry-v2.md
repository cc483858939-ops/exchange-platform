# Recommendation Telemetry v2

The recommendation API serves deterministic rules_v3. Telemetry is an attribution and measurement protocol; affinity is derived server-side from the durable facts.

## Contract

Each successful GET /api/recommendations/articles creates one UUID request ID. The same ID is embedded in every signed card token and recorded in recommendation_requests. Request persistence is best effort and does not change a successful recommendation response.

POST /api/recommendation-events accepts impression, click, read_end, feed_dwell, and not_interested. The v2 tracking token binds the authenticated user, article, request, position, ranker, strategy, token lifetime, estimated_read_time_ms, and read_policy_version. The client cannot choose those values.

A read_end payload contains only foreground_time_ms, scroll_progress_percent, and exit_type. The server derives read_outcome under read_v1:

- minimum dwell is clamp(estimated_read_time * 0.35, 3s, 20s);
- strong dwell is clamp(estimated_read_time * 0.80, 3s, 45s);
- qualified wins when strong dwell is reached, or when minimum dwell and progress of at least 50% are both reached;
- otherwise quick_bounce requires foreground time below 3 seconds and progress below 10%; all remaining reads are neutral.

A feed_dwell payload contains only the raw client-measured feed_visible_time_ms. It is valid from 1ms through 21600000ms (6 hours); the server derives all attribution from the signed tracking token and does not accept a client-generated outcome.

The server rejects malformed ranges, invalid token-bound context, and read fields on non-read events. Estimate and outcome are never accepted from the client.

## Browser measurement

The detail page measures foreground time with a monotonic performance clock. Visibility changes pause and resume the foreground clock; hiding the tab does not finish the session. Initial viewport visibility is not progress. Progress advances only on actual scroll events through the initially unread region, is monotonic, and is not advanced by resize or reflow. Short posts therefore report zero progress. Route leave and pagehide finish the session once; finish is idempotent. `read_end` remains detail-page reading telemetry and is independent of Feed dwell.

Feed dwell is enabled only for server-returned recommendation cards rendered by HomeView's active For You tab with a valid tracking token. At most one eligible card accumulates time at a time, selected by normalized viewport visibility, then closest vertical center, then stable business key. Timing starts and pauses from `performance.now()`; hidden-tab time and time outside the viewport are excluded. Temporary viewport exit pauses without finalizing, and re-entry resumes the same request/article accumulator. Terminal actions and lifecycle boundaries such as click, not-interested, removal, refresh, leaving For You, reset, pagehide, teardown, or client stop finalize at most one raw `feed_dwell` event.

Feed dwell deliberately does not instrument Following items, recently published local posts, or RecommendationView's masonry cards. Existing recommendation impressions still require raw IntersectionObserver visibility of at least 50% for at least one second. Feed dwell may therefore exist without a one-second impression, and `feed_dwell_count` may exceed impression relationships at that sub-second boundary.

## Delivery and metrics

Accepted facts are written with an outbox event, then projected through Kafka and ConsumerInbox into recommendation_daily_metrics. Feed metrics aggregate `feed_dwell_count` and total `feed_visible_time_ms` per metric dimension. The raw per-event duration remains in recommendation_events for future distribution or percentile analysis; the daily total and count must not be treated as a substitute for that distribution. Metrics use the server-derived qualified/quick-bounce outcomes and remain rebuildable from durable facts.

When frontend and backend deploy independently, rollout order is mandatory: deploy the database/backend event model and validation first, then outbox/Kafka and daily metrics consumer/rebuild support, and only then deploy the frontend Feed dwell emitter. A frontend that emits feed_dwell must not be deployed against a backend that still rejects the event type.

## Local configuration

The root D:\code\mf\docker-compose.yml supplies the recommendation signing key through RECOMMENDATION_TELEMETRY_SIGNING_KEY. AI_API_KEY belongs only to the article AI-processing worker. Keep signing keys outside source control and revoke or rotate any provider credential that was previously exposed in a committed configuration outside the repository. Token TTL and rate limits remain server configuration; read classification is fixed by read_v1. Feed dwell is raw telemetry only and is not currently consumed by rules_v3 or personalization.