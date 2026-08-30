# Long-list browser performance baseline

This is a standalone Vite benchmark for the current, non-windowed `PostCard` list. It is intentionally separate from the production Vue Router and does not modify the Home, Profile, History, Detail, telemetry, or backend paths.

Run it from `Exchangeapp_frontend`:

```bash
npm run perf:build
npm run perf:preview -- --host 127.0.0.1
```

Open `http://127.0.0.1:4174/?autorun=1` in a foreground Chromium-family browser. The runner automatically executes the desktop/mobile, tracked/untracked, and 20/50/100/200/300-card matrix. Each scenario uses a fresh, fully visible iframe, one warm-up, and three timing-valid recorded runs. The 20-card append cases, 100-card append cases, 200-card append cases, and the extra 280-card append case each measure one controlled `+20` append.

Every scenario runs a 12-interval RAF preflight before mounting, then repeats the cadence check after scrolling and after PostCard cleanup. A hidden document, median RAF interval over 50 ms, repeated intervals at or above 250 ms, or an approximately 1 Hz scroll cadence rejects that attempt as **measurement timing invalid**. A timing-invalid recorded slot is discarded and retried up to two additional times; invalid attempts never enter the aggregate and never become a RED product result. A real large gap backed by a matching Long Task remains eligible for application-performance analysis.

The child scenario mounts the real `src/components/feed/PostCard.vue` with a fresh Pinia instance, an isolated memory router, and the production tokens/base CSS. It creates deterministic local fixtures only. Tracked mode uses the real `ArticleViewTelemetryClient` with its null-viewer fallback; it instruments the native `IntersectionObserver` by delegation, and it should produce no `/article-view-events` request. Untracked mode must produce zero PostCard observer targets.

The completed suite is exposed as `window.__EXCHANGE_PERF_RESULT__` and includes raw runs, aggregated rows, tracked-versus-untracked deltas, 300/100 scaling, environment metadata, measured harness HEAD, rejected timing-attempt count, classification, bottleneck notes, and a next-step recommendation. The page also provides raw JSON, summary JSON, and Markdown copy actions.

Vitest covers fixture determinism, percentile/frame calculations, aggregation, deltas, scaling, classification logic, Markdown serialization, and postMessage validation. Vitest/jsdom timing is not used as browser-performance evidence.

The previous baseline was rejected and removed because its browser run contained approximately 1,000 ms RAF intervals caused by an invalid/throttled measurement environment. Do not treat those files as product evidence. A real Chromium-family browser must complete the autorun with three valid recorded runs for every required scenario before `perf/results/long-list-baseline.json` or `.md` is created. If any required slot exhausts its timing retries, or browser access is unavailable, keep the result files absent and report **MEASUREMENT NOT VERIFIED**.

When the harness is measured from an uncommitted working tree, `measuredHarnessHead` records the current base Git HEAD for traceability, but the result is only a candidate until the harness changes are committed by the repository owner.
