// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import type { FeedPost } from '../types/Feed';
import { usePostDetailHandoffStore } from './postDetailHandoff';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
}));

vi.mock('./auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

const basePost = (): FeedPost => ({
  id: 42,
  author: {
    id: 7,
    username: 'reader',
    display_name: 'Reader',
    avatar_url: '/reader.png',
  },
  title: 'A warm post',
  excerpt: 'A short preview',
  coverImageUrl: '/cover-a.png',
  createdAt: '2026-08-26T00:00:00.000Z',
  likeCount: 10,
  replyCount: 2,
  viewCount: 300,
  liked: true,
  likeStatus: 'ready',
  repostCount: 0,
  reposted: false,
  repostStatus: 'ready',
});

const setAuth = (id: number | null) => {
  mocks.authStore = reactive({
    isAuthenticated: id !== null,
    currentIdentity: id === null ? null : { id, username: `viewer-${id}` },
  });
};

const createStore = (id = 7) => {
  setAuth(id);
  setActivePinia(createPinia());
  return usePostDetailHandoffStore();
};

describe('postDetailHandoff store', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-26T12:00:00.000Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('clones the FeedPost and author before remembering it', () => {
    const store = createStore();
    const source = basePost();

    store.remember(source);
    source.title = 'Source mutation';
    source.author.username = 'mutated-reader';
    source.likeCount = 999;

    expect(store.pending?.post).toMatchObject({
      id: 42,
      title: 'A warm post',
      likeCount: 10,
      author: {
        username: 'reader',
      },
    });
  });

  it('consumes a matching handoff once for the same authenticated viewer', () => {
    const store = createStore();
    store.remember(basePost());

    expect(store.consume(42)).toMatchObject({
      id: 42,
      title: 'A warm post',
    });
    expect(store.consume(42)).toBeNull();
    expect(store.pending).toBeNull();
  });

  it('clears a handoff when the post ID is wrong', () => {
    const store = createStore();
    store.remember(basePost());

    expect(store.consume(43)).toBeNull();
    expect(store.pending).toBeNull();
  });

  it('clears a handoff when the authenticated viewer changes', () => {
    const store = createStore(7);
    store.remember(basePost());
    mocks.authStore.currentIdentity = { id: 8, username: 'viewer-8' };

    expect(store.consume(42)).toBeNull();
    expect(store.pending).toBeNull();
  });

  it('clears an expired handoff after thirty seconds', () => {
    const store = createStore();
    store.remember(basePost());
    vi.advanceTimersByTime(30_001);

    expect(store.consume(42)).toBeNull();
    expect(store.pending).toBeNull();
  });

  it('clears a pending handoff explicitly', () => {
    const store = createStore();
    store.remember(basePost());

    store.clear();

    expect(store.pending).toBeNull();
    expect(store.consume(42)).toBeNull();
  });
});


