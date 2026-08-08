# Recommendation Telemetry v1.1

The recommendation API remains `rules_v1`; this feature records measurable serving and engagement facts only.

## Contract

Each successful `GET /api/recommendations/articles` creates one UUID request ID. The same ID is embedded in every signed card token and recorded in `recommendation_requests`. The request record is best effort: persistence failure is logged and does not change a successful response.

`POST /api/recommendation-events` accepts `impression`, `click`, `read_end`, and `not_interested`. Tokens are HMAC-signed server attribution; the client cannot choose user, article, position, strategy, or ranker fields. `event_id` provides transport idempotency and `(request_id, article_id, event_type)` provides business uniqueness.

`read_end` requires `foreground_time_ms`, `max_scroll_depth`, and `exit_type`. A qualified read is foreground time at least 20 seconds or depth at least 50%. A quick bounce is below 5 seconds and below 25%; qualified reads take precedence. Other event types must not include read fields.

## Delivery and metrics

Accepted facts are written with an outbox event, then projected through Kafka and ConsumerInbox into `recommendation_daily_metrics`. The projection tracks impressions, clicks, qualified reads, quick bounces, and not-interested counts by day, scene, ranker, strategy, position, and article. Rebuilds derive the same metrics from durable facts.

## Frontend flow

The global client records a 50%-visible card after one second, buffers events in session storage, and retries safely. Clicking a card first saves session-only attribution, records the click, then navigates. The detail page consumes only that article's attribution, measures foreground time and depth, and sends one `read_end` on page hide or route exit. “Not interested” records an event and removes only that card from the current list.

## Local configuration

The root `D:\code\mf\docker-compose.yml` enables telemetry locally with a development-only signing key. Environment variables tune token TTL, rate limit, qualified-read thresholds, and quick-bounce thresholds. Replace the default signing key outside local development.