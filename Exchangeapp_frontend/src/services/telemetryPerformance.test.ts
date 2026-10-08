// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RecommendationTelemetryClient } from './recommendationTelemetry';
import { PostViewTelemetryClient } from './postViewTelemetry';

const mocks = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock('../axios', () => ({ default: { post: mocks.post } }));

const tracking = (id: number) => ({
  request_id: 'performance-' + id, position: 1, scene: 'recommendation_page',
  ranker_version: 'rules_v3', ranker_config_hash: 'hash', strategy_id: 'test',
  token: 'token-' + id, expires_at: '2099-01-01T00:00:00Z',
});
const payload = { foreground_time_ms: 1000, scroll_progress_percent: 50, exit_type: 'route_leave' };
const recommendationKey = 'recommendation_telemetry_queue_v2';
const viewKey = 'post_view_telemetry_queue_v1';
const saved = (key: string) => JSON.parse(sessionStorage.getItem(key) ?? '[]');

class Observer {
  static current: Observer;
  constructor(private callback: IntersectionObserverCallback) { Observer.current = this; }
  observe() {}
  unobserve() {}
  disconnect() {}
  emit(element: Element, intersecting = true) {
    this.callback([{ target: element, isIntersecting: intersecting, intersectionRatio: intersecting ? 1 : 0 } as IntersectionObserverEntry], this as unknown as IntersectionObserver);
  }
}

describe('telemetry hot-path budgets', () => {
  let recommendation: RecommendationTelemetryClient;
  let views: PostViewTelemetryClient | undefined;
  let clock = 0;
  let frame: FrameRequestCallback | undefined;

  beforeEach(() => {
    sessionStorage.clear();
    mocks.post.mockReset();
    mocks.post.mockImplementation(() => new Promise(() => {}));
    clock = 0;
    frame = undefined;
    vi.spyOn(performance, 'now').mockImplementation(() => clock);
    vi.stubGlobal('IntersectionObserver', Observer);
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation(callback => { frame = callback; return 1; });
    vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => { frame = undefined; });
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1000 });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 });
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    recommendation = new RecommendationTelemetryClient(() => null);
    recommendation.start();
  });

  afterEach(() => {
    recommendation.clearSession();
    recommendation.stop();
    views?.stop();
    views = undefined;
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  const card = () => {
    const element = document.createElement('article');
    const geometry = vi.spyOn(element, 'getBoundingClientRect').mockReturnValue({
      top: 0, bottom: 600, left: 0, right: 1000, width: 1000, height: 600,
    } as DOMRect);
    return { element, geometry };
  };

  it('does not scan detached history and reads one rectangle per active candidate', async () => {
    const detached = card();
    for (let id = 1; id <= 2000; id++) {
      recommendation.observeFeedCard(detached.element, id, tracking(id));
      recommendation.unobserveFeedCard(id, tracking(id));
    }
    const first = card();
    const second = card();
    recommendation.observeFeedCard(first.element, 3001, tracking(3001));
    recommendation.observeFeedCard(second.element, 3002, tracking(3002));
    Observer.current.emit(first.element);
    Observer.current.emit(second.element);
    first.geometry.mockClear();
    second.geometry.mockClear();
    detached.geometry.mockClear();
    const history = (recommendation as unknown as { feedDwellStates: Map<string, unknown> }).feedDwellStates;
    const historyScan = vi.spyOn(history, 'values');
    recommendation.notifyViewportChange();
    frame?.(0);
    expect(historyScan).not.toHaveBeenCalled();
    expect(first.geometry).toHaveBeenCalledTimes(1);
    expect(second.geometry).toHaveBeenCalledTimes(1);
    expect(detached.geometry).not.toHaveBeenCalled();
    clock = 1000;
    expect(recommendation.finalizeFeedDwell(3001, tracking(3001))).toBe(true);
    await Promise.resolve();
    expect(saved(recommendationKey)).toContainEqual(expect.objectContaining({ feed_visible_time_ms: 1000 }));
  });

  it('settles and removes the old candidate when one DOM element is rebound', async () => {
    const shared = card();
    recommendation.observeFeedCard(shared.element, 1, tracking(1));
    Observer.current.emit(shared.element);
    clock = 1000;
    recommendation.observeFeedCard(shared.element, 2, tracking(2));
    Observer.current.emit(shared.element);
    clock = 1500;
    expect(recommendation.finalizeFeedDwell(1, tracking(1))).toBe(true);
    expect(recommendation.finalizeFeedDwell(2, tracking(2))).toBe(true);
    await Promise.resolve();
    expect(saved(recommendationKey)).toEqual([
      expect.objectContaining({ tracking_token: 'token-1', feed_visible_time_ms: 1000 }),
      expect.objectContaining({ tracking_token: 'token-2', feed_visible_time_ms: 500 }),
    ]);
  });

  it('coalesces a burst into one write and never resurrects a cleared session', async () => {
    const writes = vi.spyOn(Storage.prototype, 'setItem');
    for (let id = 1; id <= 50; id++) recommendation.recordReadEnd(id, tracking(id), payload);
    expect(writes).not.toHaveBeenCalled();
    await Promise.resolve();
    expect(writes).toHaveBeenCalledTimes(1);
    expect(saved(recommendationKey)).toHaveLength(50);
    recommendation.recordReadEnd(51, tracking(51), payload);
    recommendation.clearSession();
    expect(saved(recommendationKey)).toEqual([]);
    await Promise.resolve();
    expect(saved(recommendationKey)).toEqual([]);
    expect(writes).toHaveBeenCalledTimes(2);
  });

  it('persists on hidden and stop even without an access token', () => {
    recommendation.recordReadEnd(1, tracking(1), payload);
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    document.dispatchEvent(new Event('visibilitychange'));
    expect(saved(recommendationKey)).toHaveLength(1);
    recommendation.recordReadEnd(2, tracking(2), payload);
    recommendation.stop();
    expect(saved(recommendationKey)).toHaveLength(2);
  });

  it('keeps Post View owners while coalescing writes and flushing pagehide', async () => {
    let owner = 7;
    views = new PostViewTelemetryClient(() => owner);
    views.start();
    const writes = vi.spyOn(Storage.prototype, 'setItem');
    for (let id = 1; id <= 50; id++) views.enqueue(id, 'event-' + id, 'feed');
    expect(writes).not.toHaveBeenCalled();
    await Promise.resolve();
    expect(writes).toHaveBeenCalledTimes(1);
    expect(saved(viewKey)).toHaveLength(50);
    owner = 8;
    views.enqueue(51, 'owner-8', 'post_detail');
    window.dispatchEvent(new Event('pagehide'));
    expect(saved(viewKey)[50]).toMatchObject({ owner_user_id: 8 });
    expect(saved(viewKey)[0]).toMatchObject({ owner_user_id: 7 });
    await Promise.resolve();
    expect(writes.mock.calls.filter(([key]) => key === viewKey)).toHaveLength(2);
  });

  it('synchronously persists terminal removals without a queued stale rewrite', async () => {
    let confirm: ((response: unknown) => void) | undefined;
    mocks.post.mockImplementationOnce(() => new Promise(resolve => { confirm = resolve; }));
    views = new PostViewTelemetryClient(() => 7);
    views.enqueue(1, 'acknowledged', 'feed');
    await Promise.resolve();
    confirm?.({ status: 202, data: { results: [{ event_id: 'acknowledged', status: 'accepted' }] } });
    await Promise.resolve();
    expect(saved(viewKey)).toEqual([]);
    await Promise.resolve();
    expect(saved(viewKey)).toEqual([]);
  });

  it('can recover from unavailable storage on the next batch', async () => {
    const writes = vi.spyOn(Storage.prototype, 'setItem').mockImplementationOnce(() => { throw new Error('privacy mode'); });
    recommendation.recordReadEnd(1, tracking(1), payload);
    await Promise.resolve();
    expect(writes).toHaveBeenCalledTimes(1);
    recommendation.recordReadEnd(2, tracking(2), payload);
    await Promise.resolve();
    expect(saved(recommendationKey)).toHaveLength(2);
  });
});
