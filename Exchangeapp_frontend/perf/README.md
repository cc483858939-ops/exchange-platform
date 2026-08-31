# Long-list browser performance baseline

This is a standalone Vite benchmark for the current, non-windowed `PostCard` list. It is intentionally separate from the production Vue Router and does not modify the Home, Profile, History, Detail, telemetry, or backend paths.

Run it from `Exchangeapp_frontend`:

```bash
npm run perf:build
npm run perf:preview -- --host 127.0.0.1
```

Open `http://127.0.0.1:4174/?autorun=1` in a foreground Chromium-family browser. The runner executes the desktop/mobile, tracked/untracked, and 20/50/100/200/300-card matrix. Each scenario is a top-level document reached with `window.location.replace`; v2 suite state and pending results are carried in the two owned `sessionStorage` keys. The 20-card append cases, 100-card append cases, 200-card append cases, and the extra 280-card append case each measure one controlled `+20` append.

The required CSS viewports are desktop `1440 × 900` and mobile `390 × 844`, with an ±8 CSS-pixel tolerance. The runner waits for a matching actual viewport and resumes after a settled resize. User agent and device pixel ratio are captured once and validated on every accepted run. A visible top-level scenario with a valid viewport and two consecutive zero-RAF preflights stops with `TOP_LEVEL_RAF_UNAVAILABLE` and does not create a performance result.

Every scenario runs a 12-interval RAF preflight before mounting, then repeats the cadence check after scrolling and after PostCard cleanup. A hidden document, median RAF interval over 50 ms, repeated intervals at or above 250 ms, or an approximately 1 Hz scroll cadence rejects that attempt as **measurement timing invalid**. A timing-invalid recorded slot is discarded and retried through the persisted cursor up to two additional times; invalid attempts never enter the aggregate and never become a RED product result. A real large gap backed by a matching Long Task remains eligible for application-performance analysis.

The scenario mounts the real `src/components/feed/PostCard.vue` with a fresh Pinia instance, an isolated memory router, and the production tokens/base CSS. It creates deterministic local fixtures only. Tracked mode uses the real `ArticleViewTelemetryClient` with its null-viewer fallback; it instruments the native `IntersectionObserver` by delegation, and it should produce no `/article-view-events` request. Untracked mode must produce zero PostCard observer targets.

The completed suite is exposed as `window.__EXCHANGE_PERF_RESULT__` and includes raw runs, aggregated rows, tracked-versus-untracked deltas, 300/100 scaling, environment metadata, measured harness HEAD, execution context, rejected timing-attempt count, classification, bottleneck notes, and a next-step recommendation. The page provides full baseline JSON, raw JSON, summary JSON, and Markdown copy actions. A completed session remains reconstructable after a runner reload until `Reset / Start New Suite` removes only the v2-owned keys.

The bottleneck label is independent from severity. Observation is a candidate only when the high-count mobile tracked 300-card row is materially worse than untracked and the 200-card comparison or tracked/untracked scaling also diverges; a small-count tracked delta alone is not enough. If tracked and untracked 300-card mounts or long tasks are both over 200 ms and comparable, the diagnostic is mounted DOM/Vue state even when the overall classification is **RED**. A healthy steady-state scroll does not establish scroll as the bottleneck when the long task is attributed to mount.

Vitest covers fixture determinism, percentile/frame calculations, aggregation, deltas, scaling, classification logic, Markdown serialization, v2 session parsing, pending-result correlation, cursor transitions, idempotency, viewport gating, environment consistency, zero-RAF termination, and full 96-slot simulation. Vitest/jsdom timing is not used as browser-performance evidence.

Create `perf/results/long-list-baseline.json` and `.md` only after a clean committed harness has completed all 96 logical slots with 72 accepted recorded runs, no failures, valid timing, valid viewports, consistent execution environments, and a matching embedded harness HEAD. If browser access is unavailable or the suite reaches `TOP_LEVEL_RAF_UNAVAILABLE`, leave result files absent and report **MEASUREMENT NOT VERIFIED**.
