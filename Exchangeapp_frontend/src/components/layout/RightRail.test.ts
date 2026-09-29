// @vitest-environment jsdom

import { createPinia, setActivePinia, type Pinia } from 'pinia';
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

let pinia: Pinia;

const mountRail = () => mount(RightRail, {
  global: {
    plugins: [pinia],
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
    pinia = createPinia();
    setActivePinia(pinia);
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
    expect(mocks.getTopics).toHaveBeenCalledOnce();
  });

  it('shows loading without hiding the Market and Exchange links', async () => {
    let resolveTopics!: (value: { items: typeof topics }) => void;
    mocks.getTopics.mockReturnValueOnce(new Promise(resolve => { resolveTopics = resolve; }));
    const wrapper = mountRail();

    await nextTick();
    expect(wrapper.get('[role="status"]').text()).toBe('Loading topics…');
    expect(wrapper.text()).toContain('MARKET');
    expect(wrapper.find('[data-exchange="true"]').exists()).toBe(true);

    resolveTopics({ items: topics });
    await flushPromises();
    expect(wrapper.findAll('.right-rail__topic')).toHaveLength(2);
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

  it('shows explicit retry after failure and updates from the shared store', async () => {
    mocks.getTopics
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ items: topics });
    const wrapper = mountRail();
    await flushPromises();

    expect(wrapper.get('[role="alert"]').text()).toBe('Topics unavailable.');
    expect(wrapper.get('button').text()).toBe('Retry');
    expect(wrapper.text()).toContain('MARKET');
    expect(wrapper.find('[data-exchange="true"]').text()).toBe('Exchange');

    await wrapper.get('button').trigger('click');
    await flushPromises();

    expect(mocks.getTopics).toHaveBeenCalledTimes(2);
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
    expect(wrapper.findAll('.right-rail__topic').map(link => link.attributes('data-slug')))
      .toEqual(['japan', 'ai']);
  });

  it('distinguishes a successful empty catalog from an error', async () => {
    mocks.getTopics.mockResolvedValueOnce({ items: [] });
    const wrapper = mountRail();
    await flushPromises();

    expect(wrapper.text()).toContain('No topics available.');
    expect(wrapper.find('button').exists()).toBe(false);
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  });
});
