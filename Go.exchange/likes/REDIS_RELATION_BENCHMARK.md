# Redis Like relation benchmark

`relation_model_benchmark_test.go` compares the previous Post → Users relation with the current User → Posts relation using the same Redis server and two edge distributions:

- `hot-post-many-users`: many users like one Post.
- `few-users-many-posts`: up to eight users like many distinct Posts.

The opt-in test records the Redis version, key count, `used_memory` and its delta from an empty DB, RSS, peak and dataset memory, allocator fragmentation, CPU counters, relation Set encodings, and the largest key's type, encoding, and memory. It also records concurrent Mutation and GetMany throughput, P95/P99 latency, and Post throughput for GetMany.

## Run safely

Use a disposable, non-zero Redis logical DB that is different from `REDIS_TEST_DB`. The test runs `FLUSHDB` before each model/scenario and again during cleanup. It refuses to run unless the explicit flush acknowledgement is set.

PowerShell example:

```powershell
$env:REDIS_LIKE_BENCH_ADDR = '127.0.0.1:6379'
$env:REDIS_LIKE_BENCH_DB = '15'
$env:REDIS_LIKE_BENCH_ALLOW_FLUSHDB = '1'
$env:REDIS_LIKE_BENCH_EDGES = '4000'
$env:REDIS_LIKE_BENCH_BATCH = '64'
$env:REDIS_LIKE_BENCH_WORKERS = '8'
go test ./likes -run '^TestRedisLikeRelationModelBenchmark$' -count=1 -v
```

Set `REDIS_LIKE_BENCH_PASSWORD` for an authenticated Redis instance. The defaults are 4,000 liked edges, GetMany batches of 64, and eight concurrent workers. Change the edge count only after confirming the Redis instance has enough memory. The old-model Lua and GetMany path live only in this benchmark file; production code uses the User → Posts model.

This benchmark is not a substitute for a production-shaped load test. Compare runs on the same host, Redis version, persistence and allocator configuration, and record those settings alongside the output. The observed `used_memory` delta is process allocator accounting after `FLUSHDB`, so run each comparison with an otherwise idle, dedicated server for the cleanest result.
