// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils';
import { nextTick } from 'vue';
import PostCard from './PostCard.vue';
import LikeAction from '../engagement/LikeAction.vue';
import RepostAction from '../engagement/RepostAction.vue';
import type { FeedPost } from '../../types/Feed';
import { formatCompactEngagementCount } from '../../utils/engagementCount';

const mocks = vi.hoisted(() => ({
  observeFeedCard: vi.fn(),
  unobserveFeedCard: vi.fn(),
  enqueue: vi.fn(),
  remember: vi.fn(),
  translatePost: vi.fn(),
}));

type ResizeObserverTestInstance = {
  trigger: () => void;
  disconnect: ReturnType<typeof vi.fn>;
};

const resizeObserverInstances: ResizeObserverTestInstance[] = [];

vi.mock('../../services/postViewTelemetry', () => ({
  getPostViewTelemetry: () => mocks,
}));

vi.mock('../../store/postDetailHandoff', () => ({
  usePostDetailHandoffStore: () => ({ remember: mocks.remember }),
}));

vi.mock('../../services/translationService', () => ({
  translatePost: mocks.translatePost,
}));

vi.mock('vue-router', () => ({
  useRouter: () => ({ resolve: () => ({ href: '/posts/42' }) }),
}));

const basePost = (): FeedPost => ({
  id: 42,
  author: {
    id: 7,
    username: 'reader',
    display_name: 'Reader',
    avatar_url: '',
  },
  content: 'Post body',
  language: 'en',
  media: [],
  createdAt: '2026-08-17T00:00:00.000Z',
  likeCount: 12,
  replyCount: 3,
  viewCount: 1234,
  liked: false,
  likeStatus: 'ready',
  repostCount: 0,
  reposted: false,
  repostStatus: 'ready',
});

describe('PostCard View metric and telemetry lifecycle', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.translatePost.mockReset();
    resizeObserverInstances.length = 0;
    vi.stubGlobal('ResizeObserver', class {
      private readonly callback: ResizeObserverCallback;
      readonly disconnect = vi.fn();

      constructor(callback: ResizeObserverCallback) {
        this.callback = callback;
        resizeObserverInstances.push({
          trigger: () => this.callback([], this as unknown as ResizeObserver),
          disconnect: this.disconnect,
        });
      }

      observe() {}
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  const mountPostCard = (post = basePost(), extraProps: Record<string, unknown> = {}) => mount(PostCard, {
    props: { post, ...extraProps },
    global: {
      stubs: {
        AuthorIdentity: { template: '<span class="author-identity" />' },
        LikeAction: {
          props: ['liked', 'count', 'disabled', 'loading', 'pending', 'ariaLabel', 'ariaPressed', 'variant'],
          emits: ['toggle'],
          template: '<button class="stub-like-action" type="button" :disabled="disabled || loading || pending" @click="$emit(\'toggle\')">{{ count }}</button>',
        },
        AppIcon: {
          props: ['name'],
          template: '<span class="test-icon" :data-icon="name" />',
        },
        ConfirmDialog: {
          props: ['title', 'description', 'confirmLabel', 'cancelLabel', 'danger', 'busy', 'error'],
          emits: ['confirm', 'cancel'],
          template: '<div class="test-confirm-dialog" role="dialog"><h2>{{ title }}</h2><p>{{ description }}</p><p v-if="error" class="test-confirm-error" role="alert">{{ error }}</p><button class="test-confirm-cancel" type="button" :disabled="busy" @click="$emit(\'cancel\')">{{ cancelLabel }}</button><button class="test-confirm-delete" type="button" :disabled="busy" @click="$emit(\'confirm\')">{{ busy ? \'Deleting…\' : confirmLabel }}</button></div>',
        },
        RouterLink: RouterLinkStub,
      },
    },
  });

  const setBodyGeometry = (
    wrapper: ReturnType<typeof mountPostCard>,
    scrollHeight: number,
    clientHeight: number,
  ) => {
    const body = wrapper.get('.post-card__body').element;
    Object.defineProperty(body, 'scrollHeight', {
      configurable: true,
      value: scrollHeight,
    });
    Object.defineProperty(body, 'clientHeight', {
      configurable: true,
      value: clientHeight,
    });
  };

  const settleBodyMeasurement = async () => {
    await nextTick();
    await nextTick();
    await nextTick();
  };

  it('renders a navigable compact View metric with the analytics icon and destination', async () => {
    const post = basePost();
    const wrapper = mountPostCard(post);
    const views = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('post-card__views'));

    expect(views?.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '42' },
    });
    expect(views?.text()).toContain(formatCompactEngagementCount(1234));
    expect(views?.attributes('aria-label')).toBe('Open post, 1,234 views');
    expect(wrapper.find('[data-icon="analytics"]').exists()).toBe(true);

    await views?.trigger('click');

    expect(mocks.remember).toHaveBeenCalledTimes(1);
    expect(mocks.remember).toHaveBeenCalledWith(post);
    expect(wrapper.emitted('postClick')).toEqual([[post]]);
    expect(wrapper.emitted('toggleLike') ?? []).toHaveLength(0);
    expect(wrapper.emitted('notInterested') ?? []).toHaveLength(0);
    expect(wrapper.emitted('deletePost') ?? []).toHaveLength(0);
    expect(mocks.enqueue).not.toHaveBeenCalled();
  });

  it('only offers translation for a different language or an undetermined source', () => {
    const sameLanguage = mountPostCard(basePost());
    expect(sameLanguage.find('.post-card__translation-action').exists()).toBe(false);
    sameLanguage.unmount();

    const differentLanguage = mountPostCard({ ...basePost(), language: 'zh' });
    expect(differentLanguage.find('.post-card__translation-action').exists()).toBe(true);
    differentLanguage.unmount();

    const anotherDifferentLanguage = mountPostCard({ ...basePost(), language: 'ja' });
    expect(anotherDifferentLanguage.find('.post-card__translation-action').exists()).toBe(true);
    anotherDifferentLanguage.unmount();

    const undeterminedLanguage = mountPostCard({ ...basePost(), language: 'und' });
    expect(undeterminedLanguage.find('.post-card__translation-action').exists()).toBe(true);
    undeterminedLanguage.unmount();

    vi.stubGlobal('navigator', {
      languages: ['zh-CN'],
      language: 'zh-CN',
    });
    const sameChineseLanguage = mountPostCard({ ...basePost(), language: 'zh' });
    expect(sameChineseLanguage.find('.post-card__translation-action').exists()).toBe(false);
    sameChineseLanguage.unmount();

    for (const language of ['en', 'ja', 'und'] as const) {
      const targetChineseLanguage = mountPostCard({ ...basePost(), language });
      expect(targetChineseLanguage.find('.post-card__translation-action').exists()).toBe(true);
      targetChineseLanguage.unmount();
    }
  });

  it('captures content, reply, and view navigation with one handoff and one postClick each', async () => {
    const post = basePost();
    const wrapper = mountPostCard(post);
    const links = wrapper.findAllComponents(RouterLinkStub);
    const content = wrapper.find('.post-card__body .linkified-text__internal');
    const reply = links.find(link => link.classes().includes('post-card__reply'))!;
    const views = links.find(link => link.classes().includes('post-card__views'))!;

    expect(reply.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '42' },
      query: { reply: '1' },
    });
    await content.trigger('click');
    await reply.trigger('click');
    await views.trigger('click');

    expect(mocks.remember).toHaveBeenCalledTimes(3);
    expect(mocks.remember).toHaveBeenNthCalledWith(1, post);
    expect(mocks.remember).toHaveBeenNthCalledWith(2, post);
    expect(mocks.remember).toHaveBeenNthCalledWith(3, post);
    expect(wrapper.emitted('postClick')).toEqual([[post], [post], [post]]);
  });

  it('does not remember a modified-click navigation', async () => {
    const post = basePost();
    const wrapper = mountPostCard(post);
    const content = wrapper.find('.post-card__body .linkified-text__internal');

    await content.trigger('click', { ctrlKey: true });

    expect(mocks.remember).not.toHaveBeenCalled();
    expect(wrapper.emitted('postClick')).toEqual([[post]]);
  });

  it('separates external URLs from Post Detail navigation', async () => {
    const post = { ...basePost(), content: 'Read https://example.com today' };
    const wrapper = mountPostCard(post);
    const external = wrapper.get('a.linkified-text__external');
    const internal = wrapper.get('a.linkified-text__internal');

    expect(external.attributes('href')).toBe('https://example.com');
    expect(wrapper.findAll('a a')).toHaveLength(0);

    await external.trigger('click');
    expect(mocks.remember).not.toHaveBeenCalled();
    expect(wrapper.emitted('postClick')).toBeUndefined();

    await internal.trigger('click');
    expect(mocks.remember).toHaveBeenCalledWith(post);
    expect(wrapper.emitted('postClick')).toEqual([[post]]);
  });

  it('keeps media as an independent Post Detail link', async () => {
    const post = {
      ...basePost(),
      media: [{ type: 'image' as const, url: '/media.png', position: 0 }],
    };
    const wrapper = mountPostCard(post);
    const cover = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('post-card__media-link'));

    expect(cover?.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '42' },
    });

    await cover?.trigger('click');

    expect(mocks.remember).toHaveBeenCalledWith(post);
    expect(wrapper.emitted('postClick')).toEqual([[post]]);
  });

  it('does not remember non-navigation like activation', async () => {
    const wrapper = mountPostCard();

    await wrapper.find('.stub-like-action').trigger('click');

    expect(mocks.remember).not.toHaveBeenCalled();
    expect(wrapper.emitted('toggleLike')).toEqual([[42]]);
  });

  it('observes on mount, replaces stale observation on id change, and unobserves on unmount', async () => {
    const wrapper = mountPostCard();
    const root = wrapper.element;
    const bodyObserver = resizeObserverInstances[0];

    expect(mocks.observeFeedCard).toHaveBeenCalledWith(root, 42);

    await wrapper.setProps({ post: { ...basePost(), id: 43 } });
    expect(mocks.unobserveFeedCard).toHaveBeenCalledWith(root);
    expect(mocks.observeFeedCard).toHaveBeenCalledWith(root, 43);

    wrapper.unmount();
    expect(mocks.unobserveFeedCard).toHaveBeenCalledWith(root);
    expect(bodyObserver?.disconnect).toHaveBeenCalledTimes(1);
  });

  it('does not show Show more when the collapsed body fits', async () => {
    const wrapper = mountPostCard();
    setBodyGeometry(wrapper, 100, 100);
    await settleBodyMeasurement();

    expect(wrapper.find('.post-card__show-more').exists()).toBe(false);
    expect(wrapper.get('.post-card__body').classes()).not.toContain('post-card__body--expanded');
    wrapper.unmount();
  });

  it('shows Show more when the collapsed body overflows', async () => {
    const wrapper = mountPostCard();
    setBodyGeometry(wrapper, 102, 100);
    resizeObserverInstances[0]?.trigger();
    await settleBodyMeasurement();

    expect(wrapper.find('.post-card__show-more').text()).toBe('Show more');
    expect(wrapper.get('.post-card__body').classes()).not.toContain('post-card__body--expanded');
    wrapper.unmount();
  });

  it('performs the initial overflow measurement without ResizeObserver', async () => {
    vi.stubGlobal('ResizeObserver', undefined);
    const wrapper = mountPostCard();
    setBodyGeometry(wrapper, 200, 100);
    await settleBodyMeasurement();

    expect(wrapper.find('.post-card__show-more').exists()).toBe(true);
    wrapper.unmount();
  });

  it('expands the body without triggering Post Detail navigation', async () => {
    const post = { ...basePost(), content: 'A long post body' };
    const wrapper = mountPostCard(post);
    setBodyGeometry(wrapper, 200, 100);
    await settleBodyMeasurement();

    await wrapper.get('.post-card__show-more').trigger('click');

    expect(wrapper.get('.post-card__body').classes()).toContain('post-card__body--expanded');
    expect(wrapper.find('.post-card__show-more').exists()).toBe(false);
    expect(wrapper.emitted('postClick')).toBeUndefined();
    expect(mocks.remember).not.toHaveBeenCalled();
  });

  it('resets expansion and remeasures when the post id changes', async () => {
    const wrapper = mountPostCard({ ...basePost(), content: 'First body' });
    setBodyGeometry(wrapper, 200, 100);
    await settleBodyMeasurement();
    await wrapper.get('.post-card__show-more').trigger('click');

    setBodyGeometry(wrapper, 100, 100);
    await wrapper.setProps({ post: { ...basePost(), id: 43, content: 'Second body' } });
    await settleBodyMeasurement();

    expect(wrapper.get('.post-card__body').classes()).not.toContain('post-card__body--expanded');
    expect(wrapper.find('.post-card__show-more').exists()).toBe(false);
  });

  it('translates on demand without replacing the original post body and supports hide/show', async () => {
    const post = { ...basePost(), language: 'und' as const };
    mocks.translatePost.mockResolvedValue({
      post_id: 42,
      source_language: 'zh',
      target_language: 'en',
      translated: true,
      translation: 'Translated post body',
    });
    const wrapper = mountPostCard(post);

    expect(wrapper.get('.post-card__translation-action').text()).toBe('Translate post');
    await wrapper.get('.post-card__translation-action').trigger('click');
    await flushPromises();

    expect(mocks.translatePost).toHaveBeenCalledWith(42, 'en');
    expect(wrapper.get('.post-card__body').text()).toContain('Post body');
    expect(wrapper.get('.post-card__translation-body').text()).toBe('Translated post body');
    expect(wrapper.get('.post-card__translation-label').text()).toBe('Translated to English');
    expect(wrapper.text()).not.toContain('Translated from');
    expect(wrapper.text()).not.toContain('detected language');
    expect(wrapper.get('.post-card__translation-action').text()).toBe('Hide translation');

    await wrapper.get('.post-card__translation-action').trigger('click');
    expect(wrapper.find('.post-card__translation-body').exists()).toBe(false);
    expect(wrapper.get('.post-card__translation-action').text()).toBe('Show translation');

    await wrapper.get('.post-card__translation-action').trigger('click');
    expect(wrapper.get('.post-card__translation-body').text()).toBe('Translated post body');
  });

  it('exposes a pending state while the translation request is in flight', async () => {
    let resolveTranslation!: (value: unknown) => void;
    mocks.translatePost.mockReturnValueOnce(new Promise(resolve => {
      resolveTranslation = resolve;
    }));
    const wrapper = mountPostCard({ ...basePost(), language: 'und' as const });

    await wrapper.get('.post-card__translation-action').trigger('click');
    await nextTick();
    expect(wrapper.get('.post-card__translation-action').text()).toBe('Translating…');
    expect(wrapper.get('.post-card__translation-action').attributes('disabled')).toBe('');

    resolveTranslation({
      post_id: 42,
      source_language: 'zh',
      target_language: 'en',
      translated: true,
      translation: 'Done',
    });
    await flushPromises();
    expect(wrapper.get('.post-card__translation-body').text()).toBe('Done');
  });

  it('shows a retry action after a translation failure', async () => {
    const post = { ...basePost(), language: 'und' as const };
    mocks.translatePost.mockRejectedValueOnce(new Error('unavailable'));
    const wrapper = mountPostCard(post);

    await wrapper.get('.post-card__translation-action').trigger('click');
    await flushPromises();
    expect(wrapper.get('.post-card__translation-action').text()).toBe('Translation unavailable · Retry');

    mocks.translatePost.mockResolvedValueOnce({
      post_id: 42,
      source_language: 'ja',
      target_language: 'en',
      translated: true,
      translation: 'Retry succeeded',
    });
    await wrapper.get('.post-card__translation-action').trigger('click');
    await flushPromises();

    expect(wrapper.get('.post-card__translation-body').text()).toBe('Retry succeeded');
    expect(mocks.translatePost).toHaveBeenCalledTimes(2);
  });

  it('drops a late translation response after the post content changes', async () => {
    let resolveTranslation!: (value: unknown) => void;
    mocks.translatePost.mockReturnValueOnce(new Promise(resolve => {
      resolveTranslation = resolve;
    }));
    const wrapper = mountPostCard({ ...basePost(), language: 'und' as const });

    await wrapper.get('.post-card__translation-action').trigger('click');
    await wrapper.setProps({
      post: { ...basePost(), language: 'und' as const, content: 'Updated post body' },
    });
    resolveTranslation({
      post_id: 42,
      source_language: 'zh',
      target_language: 'en',
      translated: true,
      translation: 'Stale translation',
    });
    await flushPromises();

    expect(wrapper.get('.post-card__body').text()).toContain('Updated post body');
    expect(wrapper.find('.post-card__translation-body').exists()).toBe(false);
    expect(wrapper.get('.post-card__translation-action').text()).toBe('Translate post');
  });

  it('resets expansion and remeasures when content changes on the same post', async () => {
    const wrapper = mountPostCard({ ...basePost(), content: 'First body' });
    setBodyGeometry(wrapper, 200, 100);
    await settleBodyMeasurement();
    await wrapper.get('.post-card__show-more').trigger('click');

    setBodyGeometry(wrapper, 100, 100);
    await wrapper.setProps({ post: { ...basePost(), content: 'Updated body' } });
    await settleBodyMeasurement();

    expect(wrapper.get('.post-card__body').classes()).not.toContain('post-card__body--expanded');
    expect(wrapper.find('.post-card__show-more').exists()).toBe(false);
  });

  it('remeasures body overflow when ResizeObserver reports a width change', async () => {
    const wrapper = mountPostCard();
    setBodyGeometry(wrapper, 100, 100);
    await settleBodyMeasurement();
    expect(wrapper.find('.post-card__show-more').exists()).toBe(false);

    setBodyGeometry(wrapper, 200, 100);
    resizeObserverInstances[0]?.trigger();
    await settleBodyMeasurement();

    expect(wrapper.find('.post-card__show-more').exists()).toBe(true);
  });

  it('does not observe feed view telemetry when trackView is false and syncs later changes', async () => {
    const wrapper = mountPostCard(basePost(), { trackView: false });
    const root = wrapper.element;

    expect(mocks.observeFeedCard).not.toHaveBeenCalled();

    await wrapper.setProps({ trackView: true });
    expect(mocks.observeFeedCard).toHaveBeenCalledWith(root, 42);

    await wrapper.setProps({ trackView: false });
    expect(mocks.unobserveFeedCard).toHaveBeenCalledWith(root);
  });

  it('maps like state to LikeAction and forwards its activation to the parent contract', async () => {
    const post = { ...basePost(), liked: true };
    const wrapper = mountPostCard(post);
    const likeAction = wrapper.findComponent(LikeAction);

    expect(likeAction.props('liked')).toBe(true);
    expect(likeAction.props('count')).toBe(12);
    expect(likeAction.props('ariaPressed')).toBe(true);
    expect(likeAction.props('loading')).toBe(false);
    expect(likeAction.props('disabled')).toBe(false);
    expect(likeAction.props('pending')).toBe(false);

    await likeAction.trigger('click');

    expect(wrapper.emitted('toggleLike')).toEqual([[42]]);
  });

  it('renders Reply, Repost, Like, Views and keeps the canonical author under repost context', async () => {
    const post = {
      ...basePost(),
      repostCount: 9,
      reposted: true,
      repostContext: {
        actor: {
          id: 11,
          username: 'alice',
          display_name: 'Alice',
          avatar_url: '',
        },
      },
    };
    const wrapper = mountPostCard(post);
    const repostAction = wrapper.findComponent(RepostAction);
    const engagement = wrapper.find('.post-card__engagement').element.children;

    expect(wrapper.find('.post-card__repost-context').text()).toBe('Alice reposted');
    expect(repostAction.props('reposted')).toBe(true);
    expect(repostAction.props('count')).toBe(9);
    expect(Array.from(engagement).map(element => element.className)).toEqual([
      'post-card__metric post-card__reply',
      'repost-action repost-action--compact repost-action--reposted',
      'stub-like-action',
      'post-card__metric post-card__views',
    ]);

    await repostAction.trigger('click');
    expect(wrapper.emitted('toggleRepost')).toEqual([[42]]);
  });

  it('renders active and tombstoned bounded references', async () => {
    const activeReference = {
      id: 9,
      deleted: false as const,
      author: {
        id: 8,
        username: 'referenced',
        display_name: 'Referenced Author',
        avatar_url: '',
      },
      content: 'Referenced post body',
      published_at: '2026-08-17T00:00:00.000Z',
      media: [],
    };
    const wrapper = mountPostCard({ ...basePost(), quotePost: activeReference });

    expect(wrapper.find('.post-card__reference-content').text()).toBe('Referenced post body');
    expect(wrapper.find('.post-card__reference-deleted').exists()).toBe(false);
    const referenceBodyLink = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('linkified-text__internal')
        && link.element.closest('.post-card__reference-content'));
    expect(referenceBodyLink?.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '9' },
    });

    await wrapper.setProps({
      post: { ...basePost(), quotePost: { id: 9, deleted: true } },
    });
    expect(wrapper.find('.post-card__reference-deleted').text()).toBe('Post unavailable');
    expect(wrapper.find('.post-card__reference-content').exists()).toBe(false);
    expect(wrapper.find('.post-card__reference-deleted a').exists()).toBe(false);
  });

  it('routes quote body and media to the referenced Post without outer handoff', async () => {
    const quotedPost = {
      id: 9,
      deleted: false as const,
      author: {
        id: 8,
        username: 'referenced',
        display_name: 'Referenced Author',
        avatar_url: '',
      },
      content: 'Referenced post body',
      published_at: '2026-08-17T00:00:00.000Z',
      media: [{ type: 'image' as const, url: '/reference.png', position: 0 }],
    };
    const post = {
      ...basePost(),
      media: [{ type: 'image' as const, url: '/outer.png', position: 0 }],
      quotePost: quotedPost,
    };
    const wrapper = mountPostCard(post);
    const referenceBodyLink = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('linkified-text__internal')
        && link.element.closest('.post-card__reference-content'))!;
    const referenceMediaLink = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('post-card__reference-media-link'))!;
    const outerMediaLink = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('post-card__media-link'))!;

    expect(referenceBodyLink.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '9' },
    });
    expect(referenceMediaLink.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '9' },
    });
    expect(outerMediaLink.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '42' },
    });

    await referenceBodyLink.trigger('click');
    await referenceMediaLink.trigger('click');

    expect(mocks.remember).not.toHaveBeenCalled();
    expect(wrapper.emitted('postClick')).toBeUndefined();

    await outerMediaLink.trigger('click');
    expect(mocks.remember).toHaveBeenCalledWith(post);
    expect(wrapper.emitted('postClick')).toEqual([[post]]);
  });

  it('routes reply references to the replied-to Post without changing outer navigation', async () => {
    const repliedToPost = {
      id: 17,
      deleted: false as const,
      author: {
        id: 8,
        username: 'parent',
        display_name: 'Parent Author',
        avatar_url: '',
      },
      content: 'Parent post body',
      published_at: '2026-08-17T00:00:00.000Z',
      media: [{ type: 'image' as const, url: '/parent.png', position: 0 }],
    };
    const wrapper = mountPostCard({ ...basePost(), replyToPost: repliedToPost });
    const referenceBodyLink = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('linkified-text__internal')
        && link.element.closest('.post-card__reference-content'))!;
    const referenceMediaLink = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('post-card__reference-media-link'))!;

    expect(referenceBodyLink.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '17' },
    });
    expect(referenceMediaLink.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '17' },
    });

    await referenceBodyLink.trigger('click');
    await referenceMediaLink.trigger('click');

    expect(mocks.remember).not.toHaveBeenCalled();
    expect(wrapper.emitted('postClick')).toBeUndefined();
  });

  it('keeps reference external URLs external and does not emit outer navigation', async () => {
    const wrapper = mountPostCard({
      ...basePost(),
      quotePost: {
        id: 9,
        deleted: false as const,
        author: {
          id: 8,
          username: 'referenced',
          display_name: 'Referenced Author',
          avatar_url: '',
        },
        content: 'Read https://example.com here',
        published_at: '2026-08-17T00:00:00.000Z',
        media: [],
      },
    });
    const external = wrapper.get('.post-card__reference-content .linkified-text__external');
    const internal = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('linkified-text__internal')
        && link.element.closest('.post-card__reference-content'))!;

    expect(external.attributes('href')).toBe('https://example.com');
    expect(internal.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '9' },
    });

    await external.trigger('click');
    expect(mocks.remember).not.toHaveBeenCalled();
    expect(wrapper.emitted('postClick')).toBeUndefined();
    expect(wrapper.findAll('a a')).toHaveLength(0);
  });

  it('maps unknown and unavailable like status without changing parent mutation logic', async () => {
    const wrapper = mountPostCard();
    const likeAction = wrapper.findComponent(LikeAction);

    await wrapper.setProps({ post: { ...basePost(), likeStatus: 'unknown' } });
    expect(likeAction.props('loading')).toBe(true);
    expect(likeAction.props('ariaPressed')).toBe(null);

    await wrapper.setProps({ post: { ...basePost(), likeStatus: 'unavailable' } });
    expect(likeAction.props('disabled')).toBe(true);
    expect(likeAction.props('loading')).toBe(false);
    expect(likeAction.props('ariaPressed')).toBe(null);
  });

  it('opens the Post confirmation from the More menu without emitting deletion', async () => {
    const wrapper = mountPostCard(basePost(), { showDelete: true });

    await wrapper.get('.post-card__more-button').trigger('click');
    await wrapper.get('.post-card__menu-item--danger').trigger('click');
    await nextTick();

    expect(wrapper.find('.post-card__menu').exists()).toBe(false);
    expect(wrapper.get('.test-confirm-dialog').text())
      .toContain('Delete post?');
    expect(wrapper.get('.test-confirm-dialog').text())
      .toContain('This post will be permanently deleted. This can’t be undone.');
    expect(wrapper.emitted('deletePost')).toBeUndefined();
    expect(mocks.remember).not.toHaveBeenCalled();
  });

  it('cancels Post deletion and restores focus to More actions', async () => {
    const wrapper = mountPostCard(basePost(), { showDelete: true });
    const moreButton = wrapper.get('.post-card__more-button').element;
    const focusSpy = vi.spyOn(HTMLButtonElement.prototype, 'focus');

    await wrapper.get('.post-card__more-button').trigger('click');
    await wrapper.get('.post-card__menu-item--danger').trigger('click');
    await wrapper.get('.test-confirm-cancel').trigger('click');
    await nextTick();

    expect(wrapper.find('.test-confirm-dialog').exists()).toBe(false);
    expect(wrapper.emitted('deletePost')).toBeUndefined();
    expect(focusSpy.mock.contexts).toContain(moreButton);
    focusSpy.mockRestore();
  });

  it('emits the Post ID exactly once only after confirmation', async () => {
    const wrapper = mountPostCard(basePost(), { showDelete: true });

    await wrapper.get('.post-card__more-button').trigger('click');
    await wrapper.get('.post-card__menu-item--danger').trigger('click');
    await wrapper.get('.test-confirm-delete').trigger('click');

    expect(wrapper.emitted('deletePost')).toEqual([[42]]);
    expect(wrapper.emitted('deletePost')).toHaveLength(1);
    expect(wrapper.find('.test-confirm-dialog').exists()).toBe(true);
  });

  it('keeps both confirmation actions unavailable while deletion is pending', async () => {
    const wrapper = mountPostCard(basePost(), { showDelete: true });

    await wrapper.get('.post-card__more-button').trigger('click');
    await wrapper.get('.post-card__menu-item--danger').trigger('click');
    await wrapper.setProps({ deletePending: true });

    expect(wrapper.get('.test-confirm-delete').attributes('disabled')).toBe('');
    expect(wrapper.get('.test-confirm-cancel').attributes('disabled')).toBe('');
    expect(wrapper.get('.test-confirm-delete').text()).toBe('Deleting…');

    await wrapper.get('.test-confirm-delete').trigger('click');
    expect(wrapper.emitted('deletePost')).toBeUndefined();
  });

  it('keeps a failed deletion error inside the open confirmation', async () => {
    const wrapper = mountPostCard(basePost(), { showDelete: true });

    await wrapper.get('.post-card__more-button').trigger('click');
    await wrapper.get('.post-card__menu-item--danger').trigger('click');
    await wrapper.setProps({ deleteError: 'Could not delete post.' });

    expect(wrapper.find('.test-confirm-dialog').exists()).toBe(true);
    expect(wrapper.get('.test-confirm-error').text()).toBe('Could not delete post.');
    expect(wrapper.find('.post-card__delete-status').exists()).toBe(false);
    expect(wrapper.find('.post-card').exists()).toBe(true);
  });

  it('closes the confirmation when the Post identity changes', async () => {
    const wrapper = mountPostCard(basePost(), { showDelete: true });

    await wrapper.get('.post-card__more-button').trigger('click');
    await wrapper.get('.post-card__menu-item--danger').trigger('click');
    await wrapper.setProps({ post: { ...basePost(), id: 43 } });
    await nextTick();

    expect(wrapper.find('.test-confirm-dialog').exists()).toBe(false);
    expect(wrapper.emitted('deletePost')).toBeUndefined();
  });

  it('forwards likePending as pending while preserving the optimistic visual props', () => {
    const wrapper = mount(PostCard, {
      props: { post: { ...basePost(), liked: true }, likePending: true },
      global: {
        stubs: {
          AuthorIdentity: { template: '<span class="author-identity" />' },
          LikeAction: {
            props: ['liked', 'count', 'disabled', 'loading', 'pending', 'ariaLabel', 'ariaPressed', 'variant'],
            template: '<button class="stub-like-action" type="button">{{ count }}</button>',
          },
          AppIcon: {
            props: ['name'],
            template: '<span class="test-icon" :data-icon="name" />',
          },
          RouterLink: RouterLinkStub,
        },
      },
    });
    const likeAction = wrapper.findComponent(LikeAction);

    expect(likeAction.props('pending')).toBe(true);
    expect(likeAction.props('liked')).toBe(true);
  });
});
