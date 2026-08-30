# Long-list browser performance baseline

This is a standalone Vite benchmark for the current, non-windowed `PostCard` list. It is intentionally separate from the production Vue Router and does not modify the Home, Profile, History, Detail, telemetry, or backend paths.

Run it from `Exchangeapp_frontend`:

```bash
npm run perf:build
npm run perf:preview -- --host 127.0.0.1
```

Open `http://127.0.0.1:4174/?autorun=1`. The runner automatically executes the desktop/mobile, tracked/untracked, and 20/50/100/200/300-card matrix. Each scenario uses a fresh iframe, one warm-up, and three recorded runs. The 20-card append cases, 100-card append cases, 200-card append cases, and the extra 280-card append case each measure one controlled `+20` append.

The child scenario mounts the real `src/components/feed/PostCard.vue` with a fresh Pinia instance, an isolated memory router, and the production tokens/base CSS. It creates deterministic local fixtures only. Tracked mode uses the real `ArticleViewTelemetryClient` with its null-viewer fallback; it instruments the native `IntersectionObserver` by delegation, and it should produce no `/article-view-events` request. Untracked mode must produce zero PostCard observer targets.

The completed suite is exposed as `window.__EXCHANGE_PERF_RESULT__` and includes raw runs, aggregated rows, tracked-versus-untracked deltas, 300/100 scaling, environment metadata, classification, bottleneck notes, and a next-step recommendation. The page also provides raw JSON, summary JSON, and Markdown copy actions.

Vitest covers fixture determinism, percentile/frame calculations, aggregation, deltas, scaling, classification logic, Markdown serialization, and postMessage validation. Vitest/jsdom timing is not used as browser-performance evidence.

No baseline result files are checked in automatically. A real Chromium-family browser must complete the autorun before `perf/results/long-list-baseline.json` or `.md` is created. If browser access is unavailable, the harness remains usable for later measurement and the result must be reported as **MEASUREMENT NOT VERIFIED**.
