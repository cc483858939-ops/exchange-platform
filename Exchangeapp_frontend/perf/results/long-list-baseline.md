# Exchange Platform Long-list Browser Performance Baseline

- Classification: **RED**
- Recommendation: LONG-LIST OPTIMIZATION REQUIRED. Write a targeted Spec N.1 for the measured bottleneck.
- Bottleneck: Feed observation lifecycle is the leading diagnostic candidate.

## Environment

- Git HEAD: ec954941dcb7612c339288644c1734d7da50c1e2
- Measured harness HEAD: ec954941dcb7612c339288644c1734d7da50c1e2
- Timestamp: 2026-08-30T14:20:13.308Z
- User agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36
- Platform: Win32
- Device pixel ratio: 1.5
- Hardware concurrency: 32
- Device memory: 16
- Long Task API: supported
- performance.memory: supported

## Timing validity

- Accepted recorded runs: 72
- Rejected timing attempts: 0
- All accepted runs timing-valid: yes
- Any accepted run lost visibility: no

## Aggregated scenarios

| Viewport | Count | Mode | Run | Runs | Mount ms | Append ms | Median frame ms | Median P95 frame ms | Worst max frame ms | Worst run >50ms % | DOM elements | Peak targets | Long tasks | Longest task ms | Heap after mount |
| --- | ---: | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| desktop | 100 | tracked | matrix | 3 | 98.9 | 23.1 | 7.6 | 8.3 | 8.9 | 0% | 5196 | 120 | 4 | 139 | 38157260 |
| desktop | 100 | untracked | matrix | 3 | 106.6 | 22.9 | 7.6 | 8.3 | 9.2 | 0% | 5196 | 0 | 3 | 116 | 37345269 |
| desktop | 20 | tracked | matrix | 3 | 38.1 | 39.2 | 7.6 | 8.3 | 8.9 | 0% | 1038 | 40 | 0 | 0 | 16994476 |
| desktop | 20 | untracked | matrix | 3 | 39.7 | 29.1 | 7.6 | 8.3 | 46.2 | 0% | 1038 | 0 | 0 | 0 | 14802858 |
| desktop | 200 | tracked | matrix | 3 | 209.8 | 43.3 | 7.6 | 8.3 | 16.2 | 0% | 10392 | 220 | 3 | 204 | 61457595 |
| desktop | 200 | untracked | matrix | 3 | 191.5 | 34.8 | 7.6 | 8.4 | 16.2 | 0% | 10392 | 0 | 3 | 222 | 51161619 |
| desktop | 280 | tracked | append | 3 | 305 | 56 | 7.6 | 8.5 | 17.1 | 0% | 14550 | 300 | 4 | 302 | 69550613 |
| desktop | 280 | untracked | append | 3 | 283.2 | 42.9 | 7.6 | 8.4 | 16.2 | 0% | 14550 | 0 | 3 | 304 | 65953448 |
| desktop | 300 | tracked | matrix | 3 | 313.3 | not supported | 7.6 | 8.6 | 24.3 | 0% | 15590 | 300 | 3 | 311 | 68347063 |
| desktop | 300 | untracked | matrix | 3 | 322.5 | not supported | 7.6 | 8.6 | 23.9 | 0% | 15590 | 0 | 3 | 333 | 68238239 |
| desktop | 50 | tracked | matrix | 3 | 71.3 | not supported | 7.6 | 8.2 | 8.8 | 0% | 2597 | 50 | 3 | 74 | 23775096 |
| desktop | 50 | untracked | matrix | 3 | 62 | not supported | 7.6 | 8.3 | 9.4 | 0% | 2597 | 0 | 2 | 66 | 14100306 |
| mobile | 100 | tracked | matrix | 3 | 96.8 | 24 | 6.1 | 6.2 | 12.1 | 0% | 5196 | 120 | 3 | 96 | 37572455 |
| mobile | 100 | untracked | matrix | 3 | 92.1 | 25.6 | 7.5 | 8.2 | 8.7 | 0% | 5196 | 0 | 3 | 92 | 32320364 |
| mobile | 20 | tracked | matrix | 3 | 38.3 | 34.1 | 7.6 | 8.35 | 321.1 | 1.79% | 1038 | 40 | 0 | 0 | 14576276 |
| mobile | 20 | untracked | matrix | 3 | 30.8 | 22.4 | 7.5 | 8.2 | 8.7 | 0% | 1038 | 0 | 0 | 0 | 14813905 |
| mobile | 200 | tracked | matrix | 3 | 167.8 | 25.8 | 6.1 | 6.2 | 18 | 0% | 10392 | 220 | 3 | 171 | 50954311 |
| mobile | 200 | untracked | matrix | 3 | 163.2 | 25.8 | 7.5 | 8.2 | 16.2 | 0% | 10392 | 0 | 3 | 156 | 50911056 |
| mobile | 280 | tracked | append | 3 | 233.2 | 32.1 | 7.55 | 8.2 | 22 | 0% | 14550 | 300 | 3 | 266 | 66099007 |
| mobile | 280 | untracked | append | 3 | 270.9 | 31 | 7.5 | 8.2 | 320.8 | 0.2% | 14550 | 0 | 4 | 287 | 66006158 |
| mobile | 300 | tracked | matrix | 3 | 260.6 | not supported | 7.6 | 8.2 | 15.6 | 0% | 15590 | 300 | 3 | 279 | 68118709 |
| mobile | 300 | untracked | matrix | 3 | 246.2 | not supported | 7.5 | 8.2 | 16 | 0% | 15590 | 0 | 3 | 285 | 68046622 |
| mobile | 50 | tracked | matrix | 3 | 50.1 | not supported | 6.1 | 6.2 | 11.9 | 0% | 2597 | 50 | 1 | 87 | 15894060 |
| mobile | 50 | untracked | matrix | 3 | 53.7 | not supported | 7.5 | 8.2 | 15.4 | 0% | 2597 | 0 | 0 | 0 | 23173775 |

## Tracked versus untracked delta

| Viewport | Count | Run | Mount delta | P95 frame delta | Long-task duration delta |
| --- | ---: | --- | ---: | ---: | ---: |
| desktop | 100 | matrix | -7.22% | 0% | 15.84% |
| desktop | 20 | matrix | -4.03% | 0% | 0% |
| desktop | 200 | matrix | 9.56% | -1.19% | -0.86% |
| desktop | 280 | append | 7.7% | 1.19% | 19.13% |
| desktop | 300 | matrix | -2.85% | 0% | -1.32% |
| desktop | 50 | matrix | 15% | -1.2% | 53.54% |
| mobile | 100 | matrix | 5.1% | -24.39% | 7.17% |
| mobile | 20 | matrix | 24.35% | 1.83% | 0% |
| mobile | 200 | matrix | 2.82% | -24.39% | 4.53% |
| mobile | 280 | append | -13.92% | 0% | -12.87% |
| mobile | 300 | matrix | 5.85% | 0% | 2.13% |
| mobile | 50 | matrix | -6.7% | -24.39% | not supported% |

## 300 / 100 scaling

| Viewport | Mode | DOM | Mount | P95 frame | Heap after mount |
| --- | --- | ---: | ---: | ---: | ---: |
| desktop | tracked | 3x | 3.17x | 1.04x | 1.79x |
| desktop | untracked | 3x | 3.03x | 1.04x | 1.83x |
| mobile | tracked | 3x | 2.69x | 1.32x | 1.81x |
| mobile | untracked | 3x | 2.67x | 1x | 2.11x |

## Failures

- None
