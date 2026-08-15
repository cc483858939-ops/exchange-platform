// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  RecommendationTelemetryClient,
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

const tracking = {
  request_id: 'request-42',
  position: 1,
  scene: 'recommendation_page',
  ranker_version: 'rules_v3',
  ranker_config_hash: 'config-hash',
  strategy_id: 'cold_start_rules_v3',
  token: 'v2.token.signature',
  expires_at: '2099-08-15T00:00:00.000Z',
};

const readPayload: RecommendationReadEndPayload = {
  foreground_time_ms: 5000,
  scroll_progress_percent: 25,
  exit_type: 'route_leave',
};

const responseFor = (events: Array<{ event_id: string }>) => ({
  accepted: events.length,
  duplicates: 0,
  rejected: 0,
  results: events.map(event => ({
    event_id: event.event_id,
    status: 'accepted' as const,
  })),
});

const eventsFromRequest = (request: RequestInit | undefined) =>
  JSON.parse(String(request?.body ?? '{}')).events as Array<{
    event_id: string;
    event_type: string;
    exit_type?: string;
  }>;

describe('RecommendationTelemetryClient delivery', () => {
  let client: RecommendationTelemetryClient | null = null;

  beforeEach(() => {
    sessionStorage.clear();
    mocks.post.mockReset();
    mocks.fetch.mockReset();
    vi.stubGlobal('fetch', mocks.fetch);
    vi.stubGlobal('crypto', { randomUUID: vi.fn(() => 'event-' + Math.random()) });
  });

  afterEach(() => {
    client?.clearSession();
    client?.stop();
    client = null;
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it('captures a read_end enqueued by a later pagehide producer', async () => {
    mocks.fetch.mockImplementation(async (_url: string, request: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () => responseFor(eventsFromRequest(request)),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token');
    client.start();

    window.addEventListener('pagehide', () => {
      client?.recordReadEnd(42, tracking, { ...readPayload, exit_type: 'page_hide' });
    }, { once: true });
    window.dispatchEvent(new Event('pagehide'));

    await Promise.resolve();
    await Promise.resolve();

    expect(mocks.fetch).toHaveBeenCalledTimes(1);
    const events = eventsFromRequest(mocks.fetch.mock.calls[0][1] as RequestInit);
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({ event_type: 'read_end', exit_type: 'page_hide' });
  });

  it('does not start Axios when recordReadEnd only enqueues an event', () => {
    client = new RecommendationTelemetryClient(() => 'Bearer test-token');

    expect(client.recordReadEnd(42, tracking, readPayload)).toBe(true);
    expect(mocks.post).not.toHaveBeenCalled();
    expect(mocks.fetch).not.toHaveBeenCalled();
  });

  it('sends the final event with keepalive while normal Axios is in flight', async () => {
    let resolveNormal: ((value: unknown) => void) | undefined;
    mocks.post.mockImplementationOnce(() => new Promise(resolve => {
      resolveNormal = resolve;
    }));
    mocks.fetch.mockImplementation(async (_url: string, request: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () => ({ ...responseFor([]), results: [] }),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token');

    client.recordClick(7, tracking);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    client.recordReadEnd(42, tracking, readPayload);
    await client.flush(true);

    const events = eventsFromRequest(mocks.fetch.mock.calls[0][1] as RequestInit);
    expect(events.some(event => event.event_type === 'read_end')).toBe(true);

    resolveNormal?.({ data: responseFor([]) });
    await Promise.resolve();
  });

  it('deduplicates repeated read_end calls by business key', async () => {
    mocks.fetch.mockImplementation(async (_url: string, request: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () => responseFor(eventsFromRequest(request)),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token');

    expect(client.recordReadEnd(42, tracking, readPayload)).toBe(true);
    expect(client.recordReadEnd(42, tracking, readPayload)).toBe(false);
    await client.flush(true);

    const events = eventsFromRequest(mocks.fetch.mock.calls[0][1] as RequestInit);
    expect(events.filter(event => event.event_type === 'read_end')).toHaveLength(1);
  });

  it('keeps route-leave delivery on the normal Axios flush path', async () => {
    mocks.post.mockImplementation(async (_url: string, body: { events: Array<{ event_id: string }> }) => ({
      data: responseFor(body.events),
    }));
    client = new RecommendationTelemetryClient(() => 'Bearer test-token');

    client.recordReadEnd(42, tracking, readPayload);
    expect(mocks.post).not.toHaveBeenCalled();
    await client.flush(false);

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post.mock.calls[0][0]).toBe('/recommendation-events');
    expect(mocks.fetch).not.toHaveBeenCalled();
  });
});

