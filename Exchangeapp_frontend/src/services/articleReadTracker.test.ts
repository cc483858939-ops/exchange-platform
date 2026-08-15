import { describe, expect, it } from 'vitest';
import {
  ArticleReadTracker,
  createArticleReadGeometry,
} from './articleReadTracker';

const geometry = (height: number, top = 0, viewportHeight = 800) =>
  createArticleReadGeometry({ top, height }, 0, viewportHeight);

describe('ArticleReadTracker', () => {
  it('keeps short-post progress at zero when initial viewport covers the body', () => {
    const tracker = new ArticleReadTracker(() => 0);
    tracker.start(geometry(500), true);
    tracker.recordScroll(900);
    expect(tracker.snapshot().scrollProgressPercent).toBe(0);
  });

  it('measures progress only through the initially unread region', () => {
    const tracker = new ArticleReadTracker(() => 0);
    tracker.start(geometry(2000), true);
    tracker.recordScroll(1400);
    expect(tracker.snapshot().scrollProgressPercent).toBe(50);
  });

  it('keeps maximum progress monotonic when scrolling upward', () => {
    const tracker = new ArticleReadTracker(() => 0);
    tracker.start(geometry(2000), true);
    tracker.recordScroll(1800);
    const maximum = tracker.snapshot().scrollProgressPercent;
    tracker.recordScroll(1000);
    expect(tracker.snapshot().scrollProgressPercent).toBe(maximum);
  });

  it('does not advance progress from resize or reflow alone', () => {
    const tracker = new ArticleReadTracker(() => 0);
    tracker.start(geometry(2000), true);
    tracker.updateGeometry({ articleTopDoc: -120, articleHeight: 1200 });
    expect(tracker.snapshot().scrollProgressPercent).toBe(0);
    tracker.updateGeometry({ articleTopDoc: 50, articleHeight: 3000 });
    expect(tracker.snapshot().scrollProgressPercent).toBe(0);
  });

  it('counts only visible foreground time with a monotonic clock', () => {
    let now = 0;
    const tracker = new ArticleReadTracker(() => now);
    tracker.start(geometry(2000), true);
    now = 5000;
    tracker.pause();
    now = 25000;
    tracker.resume();
    now = 30000;
    const payload = tracker.finish('route_leave');
    expect(payload?.foreground_time_ms).toBe(10000);
  });

  it('does not finish when the session is merely hidden', () => {
    let now = 0;
    const tracker = new ArticleReadTracker(() => now);
    tracker.start(geometry(2000), true);
    now = 5000;
    tracker.pause();
    expect(tracker.snapshot().finished).toBe(false);
    expect(tracker.finish('page_hide')?.foreground_time_ms).toBe(5000);
  });

  it('emits only one final payload', () => {
    const tracker = new ArticleReadTracker(() => 0);
    tracker.start(geometry(2000), true);
    expect(tracker.finish('route_leave')).not.toBeNull();
    expect(tracker.finish('route_leave')).toBeNull();
  });
});