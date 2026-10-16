// @vitest-environment jsdom

import { mount, RouterLinkStub } from '@vue/test-utils';
import { describe, expect, it, vi } from 'vitest';
import LinkifiedText from './LinkifiedText.vue';

const mocks = vi.hoisted(() => ({
  routerPush: vi.fn(),
  resolveMentionUsername: vi.fn(),
  messageWarning: vi.fn(),
  messageError: vi.fn(),
}));

vi.mock('element-plus', () => ({
  ElMessage: { warning: mocks.messageWarning, error: mocks.messageError },
}));

vi.mock('vue-router', async importOriginal => {
  const actual = await importOriginal<typeof import('vue-router')>();
  return { ...actual, useRouter: () => ({ push: mocks.routerPush }) };
});

vi.mock('../../services/mentionService', () => ({
  resolveMentionUsername: mocks.resolveMentionUsername,
}));

const mountLinkifiedText = (text: string, to?: { name: string; params?: Record<string, string> }) => (
  mount(LinkifiedText, {
    props: { text, ...(to ? { to } : {}) },
    global: {
      stubs: {
        RouterLink: RouterLinkStub,
      },
    },
  })
);

describe('LinkifiedText', () => {
  it('resolves a clicked exact username to the real profile ID only on click', async () => {
    mocks.routerPush.mockReset();
    mocks.resolveMentionUsername.mockReset().mockResolvedValue({
      id: 42,
      username: 'alice',
      display_name: 'Alice',
    });
    const wrapper = mountLinkifiedText('Thanks @alice and https://example.com/@not-a-user');

    expect(mocks.resolveMentionUsername).not.toHaveBeenCalled();
    mocks.messageWarning.mockReset();
    mocks.messageError.mockReset();
    expect(wrapper.findAll('.linkified-text__mention')).toHaveLength(1);
    await wrapper.get('.linkified-text__mention').trigger('click');

    expect(mocks.resolveMentionUsername).toHaveBeenCalledWith('alice', expect.any(AbortSignal));
    expect(mocks.routerPush).toHaveBeenCalledWith({ name: 'UserProfile', params: { id: '42' } });
    wrapper.unmount();
  });

  it('reports an unavailable username without navigating to a guessed profile', async () => {
    mocks.routerPush.mockReset();
    mocks.resolveMentionUsername.mockReset().mockResolvedValue(null);
    const wrapper = mountLinkifiedText('@missing');
    await wrapper.get('.linkified-text__mention').trigger('click');

    expect(mocks.routerPush).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain('Couldn’t find an active account for @missing.');
    expect(mocks.messageWarning).toHaveBeenCalledWith('This account is no longer available.');
    wrapper.unmount();
  });

  it('shows visible retry feedback for a profile lookup failure and guards duplicate clicks', async () => {
    let rejectRequest!: (error: Error) => void;
    mocks.routerPush.mockReset();
    mocks.resolveMentionUsername.mockReset().mockReturnValue(new Promise((_, reject) => {
      rejectRequest = reject;
    }));
    mocks.messageError.mockReset();
    const wrapper = mountLinkifiedText('@alice');
    const mention = wrapper.get('.linkified-text__mention');

    await mention.trigger('click');
    await mention.trigger('click');
    expect(mention.attributes('disabled')).toBeDefined();
    expect(mocks.resolveMentionUsername).toHaveBeenCalledTimes(1);
    rejectRequest(new Error('network failure'));
    await Promise.resolve();
    await Promise.resolve();

    expect(wrapper.text()).toContain('Couldn’t open @alice. Try again.');
    expect(mocks.messageError).toHaveBeenCalledWith('Couldn’t open this profile. Try again.');
    expect(mocks.routerPush).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('renders external links with safe browser-link attributes', () => {
    const wrapper = mountLinkifiedText('Visit https://example.com');
    const link = wrapper.get('a.linkified-text__external');

    expect(link.attributes('href')).toBe('https://example.com');
    expect(link.attributes('target')).toBe('_blank');
    expect(link.attributes('rel')).toBe('noopener noreferrer');
  });

  it('normalizes www links', () => {
    const wrapper = mountLinkifiedText('www.example.com');

    expect(wrapper.get('a.linkified-text__external').attributes('href'))
      .toBe('https://www.example.com');
  });

  it('uses RouterLink for plain segments when a target is provided', () => {
    const wrapper = mountLinkifiedText('Read this post', {
      name: 'PostDetail',
      params: { id: '42' },
    });

    const internal = wrapper.findComponent(RouterLinkStub);
    expect(internal.exists()).toBe(true);
    expect(internal.props('to')).toEqual({ name: 'PostDetail', params: { id: '42' } });
    expect(internal.text()).toBe('Read this post');
  });

  it('uses spans for plain segments without a target', () => {
    const wrapper = mountLinkifiedText('Read this post');

    expect(wrapper.find('a.linkified-text__internal').exists()).toBe(false);
    expect(wrapper.get('.linkified-text > span').text()).toBe('Read this post');
  });

  it('does not emit internal activation for external links', async () => {
    const wrapper = mountLinkifiedText('https://example.com');

    await wrapper.get('a.linkified-text__external').trigger('click');

    expect(wrapper.emitted('internal-activate')).toBeUndefined();
  });

  it('emits internal activation for plain text RouterLinks', async () => {
    const wrapper = mountLinkifiedText('Read this post', {
      name: 'PostDetail',
      params: { id: '42' },
    });

    await wrapper.findComponent(RouterLinkStub).trigger('click');

    expect(wrapper.emitted('internal-activate')).toHaveLength(1);
  });

  it('keeps HTML-looking input escaped', () => {
    const wrapper = mountLinkifiedText('<script>alert(1)</script>');

    expect(wrapper.find('script').exists()).toBe(false);
    expect(wrapper.text()).toBe('<script>alert(1)</script>');
  });
});
