import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ getTopics: vi.fn() }));
vi.mock('../services/topicService', () => ({ getTopics: mocks.getTopics }));

import { useTopicDirectoryStore } from './topicDirectory';

const topics = [
  { slug: 'zebra', label: 'Zebra', description: 'First from API' },
  { slug: 'ai', label: 'AI', description: 'Second from API' },
];

describe('topicDirectory store', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setActivePinia(createPinia());
  });

  it('loads once and preserves API order', async () => {
    mocks.getTopics.mockResolvedValueOnce({ items: topics });
    const store = useTopicDirectoryStore();

    expect(store.status).toBe('idle');
    await expect(store.ensureLoaded()).resolves.toBe(true);

    expect(mocks.getTopics).toHaveBeenCalledOnce();
    expect(store.items).toEqual(topics);
    expect(store.status).toBe('ready');
    expect(store.error).toBe('');
    await store.ensureLoaded();
    expect(mocks.getTopics).toHaveBeenCalledOnce();
  });

  it('joins concurrent initial requests', async () => {
    let resolveTopics!: (value: { items: typeof topics }) => void;
    mocks.getTopics.mockReturnValueOnce(new Promise(resolve => { resolveTopics = resolve; }));
    const store = useTopicDirectoryStore();

    const first = store.ensureLoaded();
    const second = store.ensureLoaded();
    expect(store.status).toBe('loading');
    expect(mocks.getTopics).toHaveBeenCalledOnce();

    resolveTopics({ items: topics });
    await expect(Promise.all([first, second])).resolves.toEqual([true, true]);
    expect(store.items).toEqual(topics);
  });

  it('caches failure until an explicit retry', async () => {
    mocks.getTopics.mockRejectedValueOnce(new Error('offline'));
    const store = useTopicDirectoryStore();

    await expect(store.ensureLoaded()).resolves.toBe(false);
    expect(store.status).toBe('error');
    expect(store.error).toBeTruthy();
    expect(store.items).toEqual([]);

    await expect(store.ensureLoaded()).resolves.toBe(false);
    expect(mocks.getTopics).toHaveBeenCalledOnce();
  });

  it('retries explicitly and clears the error on success', async () => {
    mocks.getTopics
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ items: topics });
    const store = useTopicDirectoryStore();

    await store.ensureLoaded();
    await expect(store.retry()).resolves.toBe(true);

    expect(mocks.getTopics).toHaveBeenCalledTimes(2);
    expect(store.status).toBe('ready');
    expect(store.error).toBe('');
    expect(store.items).toEqual(topics);
  });

  it('returns to retryable error after failed retries', async () => {
    mocks.getTopics.mockRejectedValue(new Error('offline'));
    const store = useTopicDirectoryStore();

    await store.ensureLoaded();
    await expect(store.retry()).resolves.toBe(false);
    expect(store.status).toBe('error');
    await expect(store.retry()).resolves.toBe(false);
    expect(mocks.getTopics).toHaveBeenCalledTimes(3);
    expect(store.status).toBe('error');
  });

  it('treats a missing items field as a successful empty catalog', async () => {
    mocks.getTopics.mockResolvedValueOnce({} as any);
    const store = useTopicDirectoryStore();

    await expect(store.ensureLoaded()).resolves.toBe(true);
    expect(store.status).toBe('ready');
    expect(store.items).toEqual([]);
    expect(store.error).toBe('');
  });
});
