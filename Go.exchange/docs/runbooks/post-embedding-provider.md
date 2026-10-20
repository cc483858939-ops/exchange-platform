# Post Embedding Provider Troubleshooting

| Failure | Consumer behavior | First checks |
| --- | --- | --- |
| HTTP 401 | Pause | Verify the API key and that the configured credential is current. |
| HTTP 403 | Pause | Check model access permissions and account policy. |
| HTTP 404 | Pause | Verify the endpoint URL and model name. |
| HTTP 400 / 422 | Pause | Check model configuration, input format, and the structured provider error code. |
| HTTP 413 | Split a multi-message batch; pause if one message still fails | Reduce batch size or inspect the single input size. |
| HTTP 429 rate limit | Retry, then cool down if retries are exhausted | Check provider limits, request concurrency, and `Retry-After`. |
| HTTP 429 quota | Pause | Check account quota, spending limits, and balance. |
| HTTP 503 | Retry, then cool down if retries are exhausted | Check provider health and its service status. |
| Provider Contract Error | Split a multi-message batch; pause if one message still fails | Inspect response JSON shape, vector dimensions, and model compatibility. |

Pause does not acknowledge the Kafka messages or advance their offsets. Static
configuration changes such as an API key may require restarting the Worker.
After recovery, check that Kafka Consumer Lag is decreasing and review the
Embedding Provider pause, failure, and recovery metrics. If the Kafka topic's
retention period has elapsed, use the existing compensation tooling to
repopulate missing embedding work.
