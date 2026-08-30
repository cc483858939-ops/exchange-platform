# Exchange Platform Long-list Browser Performance Baseline

- Classification: **RED**
- Recommendation: LONG-LIST OPTIMIZATION REQUIRED. Write a targeted Spec N.1 for the measured bottleneck.
- Bottleneck: DOM/Vue mounted-state cost is the leading diagnostic candidate.

## Environment

- Git HEAD: 4c4fab00784ffae3df75961987e1500b119a902e
- Timestamp: 2026-08-30T12:48:00.557Z
- User agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36
- Platform: Win32
- Device pixel ratio: 1.5
- Hardware concurrency: 32
- Device memory: 16
- Long Task API: supported
- performance.memory: supported

## Aggregated scenarios

| Viewport | Count | Mode | Run | Runs | Mount ms | Append ms | Median frame ms | P95 frame ms | Worst max ms | >50 ms | DOM elements | Peak targets | Long tasks | Longest task ms | Heap after mount |
| --- | ---: | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| desktop | 100 | tracked | matrix | 3 | 1973.5 | 2010.9 | 1005.85 | 1005.98 | 1006 | 100% | 5196 | 120 | 3 | 181 | 63671087 |
| desktop | 100 | untracked | matrix | 3 | 1970.8 | 2010.9 | 1005.7 | 1006.08 | 1006.2 | 100% | 5196 | 0 | 3 | 181 | 63064826 |
| desktop | 20 | tracked | matrix | 3 | 1944.2 | 2005.6 | 12.1 | 23.43 | 1006.1 | 100% | 1038 | 40 | 0 | 0 | 55404243 |
| desktop | 20 | untracked | matrix | 3 | 1953.1 | 2011.5 | 1005.85 | 1006 | 1006.1 | 100% | 1038 | 0 | 2 | 51 | 41266651 |
| desktop | 200 | tracked | matrix | 3 | 1930.2 | 2004.8 | 1005.25 | 1005.95 | 1006.2 | 100% | 10392 | 220 | 7 | 327 | 85319684 |
| desktop | 200 | untracked | matrix | 3 | 218.3 | 40.1 | 11.9 | 18.2 | 1006 | 100% | 10392 | 0 | 4 | 314 | 92664153 |
| desktop | 280 | tracked | append | 3 | 1950.2 | 2008.5 | 1005.35 | 1006.04 | 1006.1 | 100% | 14550 | 300 | 9 | 427 | 100052104 |
| desktop | 280 | untracked | append | 3 | 1960.1 | 2010.5 | 1005.85 | 1006.07 | 1006.2 | 100% | 14550 | 0 | 5 | 414 | 66078794 |
| desktop | 300 | tracked | matrix | 3 | 1912.2 | not supported | 1005.85 | 1005.99 | 1006.1 | 100% | 15590 | 300 | 8 | 407 | 100445660 |
| desktop | 300 | untracked | matrix | 3 | 1918.3 | not supported | 1005.5 | 1005.9 | 1005.9 | 100% | 15590 | 0 | 7 | 474 | 98587811 |
| desktop | 50 | tracked | matrix | 3 | 1947 | not supported | 1005.9 | 1006.09 | 1006.2 | 100% | 2597 | 50 | 3 | 96 | 45569219 |
| desktop | 50 | untracked | matrix | 3 | 1972.7 | not supported | 1005.85 | 1005.9 | 1006 | 100% | 2597 | 0 | 3 | 80 | 44177971 |
| mobile | 100 | tracked | matrix | 3 | 135 | 31.4 | 6.1 | 12.29 | 1005.7 | 40% | 5196 | 120 | 3 | 114 | 68152318 |
| mobile | 100 | untracked | matrix | 3 | 1964.1 | 2011.2 | 1005.75 | 1005.98 | 1006.2 | 100% | 5196 | 0 | 3 | 140 | 49012882 |
| mobile | 20 | tracked | matrix | 3 | 1975.8 | 2011.4 | 1005.8 | 1005.98 | 1006 | 100% | 1038 | 40 | 0 | 0 | 48205369 |
| mobile | 20 | untracked | matrix | 3 | 1964.9 | 2011.7 | 1005.9 | 1006 | 1006.1 | 100% | 1038 | 0 | 1 | 66 | 41330154 |
| mobile | 200 | tracked | matrix | 3 | 1967.1 | 2010.8 | 1005.75 | 1005.97 | 1006.1 | 100% | 10392 | 220 | 5 | 275 | 58393120 |
| mobile | 200 | untracked | matrix | 3 | 1962.4 | 2010.4 | 1005.1 | 1005.9 | 1006 | 100% | 10392 | 0 | 3 | 261 | 58390850 |
| mobile | 280 | tracked | append | 3 | 1956.3 | 2010.1 | 1005.9 | 1006.07 | 1006.1 | 100% | 14550 | 300 | 8 | 409 | 66159033 |
| mobile | 280 | untracked | append | 3 | 1955.4 | 2004.9 | 1005.9 | 1005.99 | 1006.1 | 100% | 14550 | 0 | 6 | 423 | 66127388 |
| mobile | 300 | tracked | matrix | 3 | 1956.5 | not supported | 1005.8 | 1005.94 | 1006 | 100% | 15590 | 300 | 8 | 412 | 67859934 |
| mobile | 300 | untracked | matrix | 3 | 1971 | not supported | 1005.85 | 1005.9 | 1006 | 100% | 15590 | 0 | 8 | 418 | 67842972 |
| mobile | 50 | tracked | matrix | 3 | 1972.4 | not supported | 1005.9 | 1005.99 | 1006 | 100% | 2597 | 50 | 3 | 82 | 44221200 |
| mobile | 50 | untracked | matrix | 3 | 1974.7 | not supported | 1005.9 | 1006 | 1006.2 | 100% | 2597 | 0 | 3 | 79 | 44244242 |

## Tracked versus untracked delta

| Viewport | Count | Run | Mount delta | P95 frame delta | Long-task duration delta |
| --- | ---: | --- | ---: | ---: | ---: |
| desktop | 100 | matrix | 0.14% | -0.01% | 3.54% |
| desktop | 20 | matrix | -0.46% | -97.67% | -100% |
| desktop | 200 | matrix | 784.2% | 5427.2% | 71.85% |
| desktop | 280 | append | -0.51% | 0% | 36.39% |
| desktop | 300 | matrix | -0.32% | 0.01% | -4.82% |
| desktop | 50 | matrix | -1.3% | 0.02% | 3.08% |
| mobile | 100 | matrix | -93.13% | -98.78% | -25.06% |
| mobile | 20 | matrix | 0.55% | 0% | -100% |
| mobile | 200 | matrix | 0.24% | 0.01% | 16.2% |
| mobile | 280 | append | 0.05% | 0.01% | 5.54% |
| mobile | 300 | matrix | -0.74% | 0% | -2.54% |
| mobile | 50 | matrix | -0.12% | 0% | 0.9% |

## 300 / 100 scaling

| Viewport | Mode | DOM | Mount | P95 frame | Heap after mount |
| --- | --- | ---: | ---: | ---: | ---: |
| desktop | tracked | 3x | 0.97x | 1x | 1.58x |
| desktop | untracked | 3x | 0.97x | 1x | 1.56x |
| mobile | tracked | 3x | 14.49x | 81.85x | 1x |
| mobile | untracked | 3x | 1x | 1x | 1.38x |

## Failures

- None
