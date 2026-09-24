// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { nextTick, reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ getTopics: vi.fn(), route: null as any }));

vi.mock('../../services/topicService', () => ({ getTopics: mocks.getTopics }));
vi.mock('vue-router', async importOriginal => {
  const actual = await importOriginal<typeof import('vue-router')>();

  return {
    ...actual,
    useRoute: () => mocks.route,
  };
});

import RightRail from './RightRail.vue';

const topics = [
  { slug: 'japan', label: 'Japan', description: 'Life in Japan' },
  { slug: 'ai', label: 'AI', description: 'Tools and models' },
];

const mountRail = () => mount(RightRail, {
  global: {
    stubs: {
      RouterLink: {
        props: ['to', 'replace'],
        template: `
          <a
            :data-name="to.name"
            :data-slug="to.params?.slug"
            :data-replace="replace ? 'true' : 'false'"
            :data-exchange="to.name === 'CurrencyExchange' ? 'true' : undefined"
          >
            <slot />
          </a>
        `,
      },
    },
  },
});

describe('RightRail Topics', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.route = reactive({ name: 'Home' });
    mocks.getTopics.mockResolvedValue({ items: topics });
  });

  it('renders topics in API order with descriptions and named route targets', async () => {
    const wrapper = mountRail();
    await flushPromises();

    const links = wrapper.findAll('.right-rail__topic');
    expect(links.map(link => link.text())).toEqual(['#JapanLife in Japan', '#AITools and models']);
    expect(links.map(link => link.attributes('data-slug'))).toEqual(['japan', 'ai']);
    expect(links.every(link => link.attributes('data-name') === 'Topic')).toBe(true);
    expect(links.every(link => link.attributes('data-replace') === 'false')).toBe(true);
    expect(wrapper.find('[data-exchange="true"]').text()).toBe('Exchange');
    expect(wrapper.attributes('aria-label')).toContain('Explore');
  });

  it('does not replace topic navigation when the current route is not a topic', async () => {
    mocks.route.name = 'Home';
    const wrapper = mountRail();
    await flushPromises();

    const links = wrapper.findAll('.right-rail__topic');
    expect(links.map(link => link.attributes('data-name'))).toEqual(['Topic', 'Topic']);
    expect(links.map(link => link.attributes('data-slug'))).toEqual(['japan', 'ai']);
    expect(links.every(link => link.attributes('data-replace') === 'false')).toBe(true);
  });

  it('replaces topic navigation when switching between topics', async () => {
    const wrapper = mountRail();
    await flushPromises();

    const aiLink = wrapper.findAll('.right-rail__topic').find(link => link.attributes('data-slug') === 'ai');
    expect(aiLink).toBeDefined();
    expect(aiLink?.attributes('data-replace')).toBe('false');

    mocks.route.name = 'Topic';
    await nextTick();

    expect(aiLink?.attributes('data-replace')).toBe('true');
  });

  it('keeps Market and Exchange visible when the topic request fails', async () => {
    mocks.getTopics.mockRejectedValueOnce(new Error('offline'));
    const wrapper = mountRail();
    await flushPromises();

    expect(wrapper.get('[role="status"]').text()).toBe('Topics unavailable');
    expect(wrapper.text()).toContain('MARKET');
    expect(wrapper.find('[data-exchange="true"]').text()).toBe('Exchange');
  });
});
