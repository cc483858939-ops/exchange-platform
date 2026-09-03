// @vitest-environment jsdom

import { mount, RouterLinkStub } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import LinkifiedText from './LinkifiedText.vue';

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
