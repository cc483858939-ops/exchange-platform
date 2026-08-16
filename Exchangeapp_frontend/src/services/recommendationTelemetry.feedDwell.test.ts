// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  RecommendationTelemetryClient,
  calculateViewportVisibility,
  type RecommendationReadEndPayload,
} from './recommendationTelemetry';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
  fetch: vi.fn(),
}));

vi.mock('../axios', () => ({
  default: {
    post: mocks.post,
  },
}));

const makeTracking = (requestID = 'request-42') => ({
  request_id: requestID,
  position: 1,
  scene: 'recommendation_page',
  ranker_version: 'rules_v3',
  ranker_config_hash: 'config-hash',
  strategy_id: 'cold_start_rules_v3',
  token: 'v2.token.signature-' + requestID,
  expires_at: '2099-08-15T00:00:00.000Z',
});

const readPayload: RecommendationReadEndPayload = {
  foreground_time_ms: 5000,
  scroll_progress_percent: 25,
  exit_type: 'route_leave',
};

const responseFor = (events: Array<Record<string, unknown>>) => ({
  accepted: events.length,
  duplicates: 0,
  rejected: 0,
  results: events.map(event => ({
    event_id: String(event.event_id ?? ''),
    status: 'accepted' as const,
  })),
});

const eventsFromAxios = (call: unknown[]) =>
  (call[1] as { events: Array<Record<string, unknown>> }).events;

const eventsFromFetch = (call: unknown[]) =>
  JSON.parse(String((call[1] as RequestInit).body ?? '{}')).events as Array<Record<string, unknown>>;

const makeRect = (left: number, top: number, width: number, height: number): DOMRect => ({
  bottom: top + height,
  height,
  left,
  right: left + width,
  top,
  width,
  x: left,
  y: top,
  toJSON: () => ({}),
} as DOMRect);

class FakeIntersectionObserver {
  static instances: FakeIntersectionObserver[] = [];

  readonly observed = new Set<Element>();
  readonly unobserved: Element[] = [];

  constructor(private readonly callback: IntersectionObserverCallback) {
    FakeIntersectionObserver.instances.push(this);
  }

  observe(element: Element) {
    this.observed.add(element);
  }

  unobserve(element: Element) {
    this.observed.delete(element);
    this.unobserved.push(element);
  }

  disconnect() {
    this.observed.clear();
  }

  takeRecords(): IntersectionObserverEntry[] {
    return [];
  }

  emit(element: Element, intersectionRatio = 1, isIntersecting = intersectionRatio > 0) {
    const entry = {
      boundingClientRect: element.getBoundingClientRect(),
      intersectionRatio,
      isIntersecting,
      rootBounds: null,
      target: element,
      time: 0,
      intersectionRect: element.getBoundingClientRect(),
    } as IntersectionObserverEntry;
    this.callback([entry], this as unknown as IntersectionObserver);
  }
}

const originalRAF = Object.getOwnPropertyDescriptor(window, 'requestAnimationFrame');
const originalCancelRAF = Object.getOwnPropertyDescriptor(window, 'cancelAnimationFrame');

describe('Feed dwell viewport helper', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1000 });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 });
  });

  it('normalizes visibility for cards larger than the viewport', () => {
    const card = document.createElement('article');
    vi.spyOn(card, 'getBoundingClientRect').mockReturnValue(makeRect(0, 200, 1000, 1600));

    expect(calculateViewportVisibility(card)).toBeCloseTo(0.75);
  });

  it('returns zero for an invalid denominator', () => {
    const card = document.createElement('article');
    vi.spyOn(card, 'getBoundingClientRect').mockReturnValue(makeRect(0, 0, 0, 100));

    expect(calculateViewportVisibility(card)).toBe(0);
  });
});

describe('RecommendationTelemetryClient Feed dwell', () => {
  let client: RecommendationTelemetryClient | null = null;
  let clock = 0;
  let rafID = 0;
  let rafCallbacks = new Map<number, FrameRequestCallback>();

  const setVisibility = (state: 'visible' | 'hidden') => {
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      value: state,
    });
  };

  const makeCard = (rect: DOMRect) => {
    let currentRect = rect;
    const element = document.createElement('article');
    vi.spyOn(element, 'getBoundingClientRect').mockImplementation(() => currentRect);
    document.body.append(element);
    return {
      element,
      setRect(nextRect: DOMRect) {
        currentRect = nextRect;
      },
    };
  };

  const observer = () => {
    const instance = FakeIntersectionObserver.instances.at(-1);
    if (!instance) {
      throw new Error('Feed dwell observer was not created');
    }
    return instance;
  };

  const runRAF = () => {
    const pending = [...rafCallbacks.values()];
    rafCallbacks.clear();
    pending.forEach(callback => callback(0));
  };

  const createClient = () => {
    client = new RecommendationTelemetryClient(() => 'Bearer test-token');
    client.start();
    return client;
  };

  const flushKeepaliveEvents = async () => {
    await client?.flush(true);
    const calls = mocks.fetch.mock.calls;
    return calls.length === 0 ? [] : eventsFromFetch(calls.at(-1) as unknown[]);
  };

  beforeEach(() => {
    clock = 0;
    rafID = 0;
    rafCallbacks = new Map();
    FakeIntersectionObserver.instances.length = 0;
    document.body.innerHTML = '';
    sessionStorage.clear();
    mocks.post.mockReset();
    mocks.fetch.mockReset();
    vi.useFakeTimers();
    vi.spyOn(performance, 'now').mockImplementation(() => clock);
    vi.stubGlobal('crypto', { randomUUID: vi.fn(() => 'event-' + Math.random()) });
    vi.stubGlobal('fetch', mocks.fetch);
    vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
    setVisibility('visible');
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1000 });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 });
    Object.defineProperty(window, 'requestAnimationFrame', {
      configurable: true,
      value: vi.fn((callback: FrameRequestCallback) => {
        const id = ++rafID;
        rafCallbacks.set(id, callback);
        return id;
      }),
    });
    Object.defineProperty(window, 'cancelAnimationFrame', {
      configurable: true,
      value: vi.fn((id: number) => {
        rafCallbacks.delete(id);
      }),
    });
  });

  afterEach(() => {
    client?.clearSession();
    client?.stop();
    client = null;
    document.body.innerHTML = '';
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    if (originalRAF) {
      Object.defineProperty(window, 'requestAnimationFrame', originalRAF);
    } else {
      Reflect.deleteProperty(window, 'requestAnimationFrame');
    }
    if (originalCancelRAF) {
      Object.defineProperty(window, 'cancelAnimationFrame', originalCancelRAF);
    } else {
      Reflect.deleteProperty(window, 'cancelAnimationFrame');
    }
  });

  it('measures basic dwell with one active eligible card', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    telemetry.observeFeedCard(card.element, 42, tracking);
    observer().emit(card.element);
    clock = 3000;
    expect(telemetry.finalizeFeedDwell(42, tracking)).toBe(true);
    const events = await flushKeepaliveEvents();

    expect(events).toEqual([expect.objectContaining({
      event_type: 'feed_dwell',
      feed_visible_time_ms: 3000,
    })]);
  });

  it('does not accumulate below normalized visibility threshold', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 700, 1000, 400));
    const tracking = makeTracking();

    telemetry.observeFeedCard(card.element, 42, tracking);
    observer().emit(card.element);
    runRAF();
    clock = 3000;

    expect(telemetry.finalizeFeedDwell(42, tracking)).toBe(false);
    expect(await flushKeepaliveEvents()).toEqual([]);
  });

  it('selects only one active card when two are eligible', async () => {
    const telemetry = createClient();
    const first = makeCard(makeRect(0, 0, 1000, 600));
    const second = makeCard(makeRect(0, 100, 1000, 600));
    const firstTracking = makeTracking('request-a');
    const secondTracking = makeTracking('request-b');

    telemetry.observeFeedCard(first.element, 42, firstTracking);
    telemetry.observeFeedCard(second.element, 43, secondTracking);
    observer().emit(first.element);
    observer().emit(second.element);
    clock = 3000;

    expect(telemetry.finalizeFeedDwell(42, firstTracking)).toBe(false);
    expect(telemetry.finalizeFeedDwell(43, secondTracking)).toBe(true);
    const events = await flushKeepaliveEvents();

    expect(events).toEqual([expect.objectContaining({
      event_type: 'feed_dwell',
      feed_visible_time_ms: 3000,
    })]);
    expect(events[0].tracking_token).toContain('request-b');
  });

  it('settles the previous card when the active card switches', async () => {
    const telemetry = createClient();
    const first = makeCard(makeRect(0, 0, 1000, 600));
    const second = makeCard(makeRect(0, 700, 1000, 400));
    const firstTracking = makeTracking('request-a');
    const secondTracking = makeTracking('request-b');

    telemetry.observeFeedCard(first.element, 42, firstTracking);
    telemetry.observeFeedCard(second.element, 43, secondTracking);
    observer().emit(first.element);
    clock = 2000;

    second.setRect(makeRect(0, 100, 1000, 600));
    observer().emit(second.element);
    clock = 5000;

    expect(telemetry.finalizeFeedDwell(42, firstTracking)).toBe(true);
    expect(telemetry.finalizeFeedDwell(43, secondTracking)).toBe(true);
    const events = await flushKeepaliveEvents();

    expect(events.map(event => event.feed_visible_time_ms).sort()).toEqual([2000, 3000]);
  });

  it('pauses offscreen and resumes the same accumulator on re-entry', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    telemetry.observeFeedCard(card.element, 42, tracking);
    observer().emit(card.element);
    clock = 2000;

    card.setRect(makeRect(0, 900, 1000, 600));
    observer().emit(card.element, 0, false);
    clock = 7000;

    card.setRect(makeRect(0, 0, 1000, 600));
    observer().emit(card.element);
    clock = 11000;

    expect(telemetry.finalizeFeedDwell(42, tracking)).toBe(true);
    const events = await flushKeepaliveEvents();

    expect(events).toEqual([expect.objectContaining({
      feed_visible_time_ms: 6000,
    })]);
  });

  it('pauses background time and resumes after visibility returns', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    telemetry.observeFeedCard(card.element, 42, tracking);
    observer().emit(card.element);
    clock = 2000;

    setVisibility('hidden');
    document.dispatchEvent(new Event('visibilitychange'));
    clock = 12000;
    setVisibility('visible');
    document.dispatchEvent(new Event('visibilitychange'));
    clock = 15000;

    expect(telemetry.finalizeFeedDwell(42, tracking)).toBe(true);
    const events = await flushKeepaliveEvents();

    expect(events).toEqual([expect.objectContaining({
      feed_visible_time_ms: 5000,
    })]);
  });

  it('enqueues dwell before click and flushes the pair', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    telemetry.observeFeedCard(card.element, 42, tracking);
    observer().emit(card.element);
    clock = 4000;

    telemetry.recordClick(42, tracking);
    const events = eventsFromAxios(mocks.post.mock.calls[0] as unknown[]);

    expect(events.map(event => event.event_type)).toEqual(['feed_dwell', 'click']);
  });

  it('enqueues dwell before not interested', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    telemetry.observeFeedCard(card.element, 42, tracking);
    observer().emit(card.element);
    clock = 4000;

    telemetry.recordNotInterested(42, tracking);
    const events = eventsFromAxios(mocks.post.mock.calls[0] as unknown[]);

    expect(events.map(event => event.event_type)).toEqual(['feed_dwell', 'not_interested']);
  });

  it('re-observing the same key and element is idempotent', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    telemetry.observeFeedCard(card.element, 42, tracking);
    observer().emit(card.element);
    clock = 1000;
    telemetry.observeFeedCard(card.element, 42, tracking);
    clock = 3000;

    expect(telemetry.finalizeFeedDwell(42, tracking)).toBe(true);
    const events = await flushKeepaliveEvents();
    expect(events).toEqual([expect.objectContaining({ feed_visible_time_ms: 3000 })]);
  });

  it('preserves accumulated dwell when the element is rebound', async () => {
    const telemetry = createClient();
    const oldCard = makeCard(makeRect(0, 0, 1000, 600));
    const newCard = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    telemetry.observeFeedCard(oldCard.element, 42, tracking);
    observer().emit(oldCard.element);
    clock = 2000;

    telemetry.observeFeedCard(newCard.element, 42, tracking);
    expect(observer().unobserved).toContain(oldCard.element);
    observer().emit(newCard.element);
    clock = 5000;

    expect(telemetry.finalizeFeedDwell(42, tracking)).toBe(true);
    const events = await flushKeepaliveEvents();

    expect(events).toEqual([expect.objectContaining({
      feed_visible_time_ms: 5000,
    })]);
  });

  it('ignores stale intersection callbacks after a Feed element rebind', async () => {
    const telemetry = createClient();
    const oldCard = makeCard(makeRect(0, 0, 1000, 600));
    const newCard = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    mocks.fetch.mockImplementation(async (_url: string, request: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () => responseFor(eventsFromFetch([_url, request])),
    }));
    telemetry.observeFeedCard(oldCard.element, 42, tracking);
    const feedObserver = observer();
    feedObserver.emit(oldCard.element);

    telemetry.observeFeedCard(newCard.element, 42, tracking);
    expect(feedObserver.unobserved).toContain(oldCard.element);

    feedObserver.emit(oldCard.element);
    vi.advanceTimersByTime(1100);
    expect(await flushKeepaliveEvents()).toEqual([]);

    feedObserver.emit(newCard.element);
    vi.advanceTimersByTime(1000);
    const events = await flushKeepaliveEvents();
    expect(events.map(event => event.event_type)).toEqual(['impression']);
  });

  it('ignores stale intersection callbacks after Feed detach', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    mocks.fetch.mockImplementation(async (_url: string, request: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () => responseFor(eventsFromFetch([_url, request])),
    }));
    telemetry.observeFeedCard(card.element, 42, tracking);
    const feedObserver = observer();
    feedObserver.emit(card.element);
    clock = 1000;

    telemetry.detachFeedCard(42, tracking);
    feedObserver.emit(card.element);
    vi.advanceTimersByTime(1100);
    expect(await flushKeepaliveEvents()).toEqual([]);

    clock = 4000;
    expect(telemetry.finalizeFeedDwell(42, tracking)).toBe(true);
    const events = await flushKeepaliveEvents();
    expect(events).toEqual([expect.objectContaining({
      event_type: 'feed_dwell',
      feed_visible_time_ms: 1000,
    })]);
  });
  it('finalizes unfinished dwell before resetObservedCards disconnects', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    telemetry.observeFeedCard(card.element, 42, tracking);
    observer().emit(card.element);
    clock = 2500;
    telemetry.resetObservedCards(true);

    const events = await flushKeepaliveEvents();
    expect(events).toEqual([expect.objectContaining({
      feed_visible_time_ms: 2500,
    })]);
  });

  it('clearSession discards unfinished dwell without creating telemetry', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    telemetry.observeFeedCard(card.element, 42, tracking);
    observer().emit(card.element);
    clock = 2000;
    telemetry.clearSession();

    expect(await flushKeepaliveEvents()).toEqual([]);
    expect(JSON.parse(sessionStorage.getItem('recommendation_telemetry_queue_v2') ?? '[]')).toEqual([]);
  });

  it('stop finalizes dwell before keepalive flush', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    mocks.fetch.mockImplementation(async (_url: string, request: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () => responseFor(eventsFromFetch([_url, request])),
    }));
    telemetry.observeFeedCard(card.element, 42, tracking);
    observer().emit(card.element);
    clock = 3500;

    telemetry.stop();
    await Promise.resolve();

    const events = eventsFromFetch(mocks.fetch.mock.calls[0] as unknown[]);
    expect(events).toEqual([expect.objectContaining({
      event_type: 'feed_dwell',
      feed_visible_time_ms: 3500,
    })]);
  });

  it('pagehide finalizes Feed dwell before a later read_end producer and keepalive flush', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    mocks.fetch.mockImplementation(async (_url: string, request: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () => responseFor(eventsFromFetch([_url, request])),
    }));
    telemetry.observeFeedCard(card.element, 42, tracking);
    observer().emit(card.element);
    window.addEventListener('pagehide', () => {
      telemetry.recordReadEnd(42, tracking, { ...readPayload, exit_type: 'page_hide' });
    }, { once: true });
    clock = 3000;

    window.dispatchEvent(new Event('pagehide'));
    await Promise.resolve();
    await Promise.resolve();

    const events = eventsFromFetch(mocks.fetch.mock.calls[0] as unknown[]);
    expect(events.map(event => event.event_type)).toEqual(['feed_dwell', 'read_end']);
  });

  it('ordinary observeCard does not create Feed dwell', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    telemetry.observeCard(card.element, 42, tracking);
    observer().emit(card.element);
    clock = 4000;
    telemetry.recordClick(42, tracking);
    await telemetry.flush(true);

    const calls = mocks.fetch.mock.calls;
    if (calls.length > 0) {
      const events = eventsFromFetch(calls[0] as unknown[]);
      expect(events.map(event => event.event_type)).toEqual(['click']);
    } else {
      expect(mocks.post).toHaveBeenCalled();
      expect(eventsFromAxios(mocks.post.mock.calls[0] as unknown[]).map(event => event.event_type)).toEqual(['click']);
    }
  });

  it('allows a short dwell before the one-second impression rule', async () => {
    const telemetry = createClient();
    const card = makeCard(makeRect(0, 0, 1000, 600));
    const tracking = makeTracking();

    telemetry.observeFeedCard(card.element, 42, tracking);
    observer().emit(card.element);
    clock = 400;
    expect(telemetry.finalizeFeedDwell(42, tracking)).toBe(true);

    const events = await flushKeepaliveEvents();
    expect(events).toEqual([expect.objectContaining({
      event_type: 'feed_dwell',
      feed_visible_time_ms: 400,
    })]);
  });

  it('throttles scroll and resize reconciliation through one RAF', () => {
    const telemetry = createClient();

    window.dispatchEvent(new Event('scroll'));
    window.dispatchEvent(new Event('scroll'));
    window.dispatchEvent(new Event('resize'));

    expect(window.requestAnimationFrame).toHaveBeenCalledTimes(1);
    runRAF();
    expect(window.requestAnimationFrame).toHaveBeenCalledTimes(1);
    telemetry.resetObservedCards(false);
  });
});