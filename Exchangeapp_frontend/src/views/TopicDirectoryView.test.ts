// @vitest-environment jsdom

import { createPinia, setActivePinia, type Pinia } from 'pinia';
import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ getTopics: vi.fn(), route: null as any }));
vi.mock('../services/topicService', () => ({ getTopics: mocks.getTopics }));
vi.mock('vue-router', async importOriginal => {
  const actual = await importOriginal<typeof import('vue-router')>();
  return { ...actual, useRoute: () => mocks.route };
});

import RightRail from '../components/layout/RightRail.vue';
import TopicDirectoryView from './TopicDirectoryView.vue';

const topics = [
  { slug: 'japan', label: 'Japan', description: 'Life in Japan' },
  { slug: 'ai', label: 'AI', description: 'Tools and models' },
];

let pinia: Pinia;

const routerLinkStub = {
  props: ['to', 'replace'],
  template: '<a :data-name="to.name" :data-slug="to.params?.slug" :data-replace="replace ? \'true\' : \'false\'" :data-exchange="to.name === \'CurrencyExchange\' ? \'true\' : undefined"><slot /></a>',
};

const mountDirectory = () => mount(TopicDirectoryView, {
  global: { plugins: [pinia], stubs: { RouterLink: routerLinkStub } },
});

const mountRail = () => mount(RightRail, {
  global: { plugins: [pinia], stubs: { RouterLink: routerLinkStub } },
});

describe('TopicDirectoryView', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    pinia = createPinia();
    setActivePinia(pinia);
    mocks.route = reactive({ name: 'Home' });
    mocks.getTopics.mockResolvedValue({ items: topics });
  });

  it('shows loading while the shared request is pending', async () => {
    mocks.getTopics.mockReturnValueOnce(new Promise(() => {}));
    const wrapper = mountDirectory();
    await flushPromises();

    expect(wrapper.get('[role="status"]').text()).toBe('Loading topics…');
    expect(wrapper.text()).not.toContain('No topics available right now.');
  });

  it('renders topic labels and descriptions as ordered Topic links', async () => {
    const wrapper = mountDirectory();
    await flushPromises();

    const links = wrapper.findAll('.topic-directory-view__item');
    expect(links.map(link => link.text())).toEqual(['#JapanLife in Japan', '#AITools and models']);
    expect(links.map(link => link.attributes('data-name'))).toEqual(['Topic', 'Topic']);
    expect(links.map(link => link.attributes('data-slug'))).toEqual(['japan', 'ai']);
  });

  it('renders a distinct empty state for an empty successful response', async () => {
    mocks.getTopics.mockResolvedValueOnce({ items: [] });
    const wrapper = mountDirectory();
    await flushPromises();

    expect(wrapper.text()).toContain('No topics available right now.');
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
    expect(wrapper.find('button').exists()).toBe(false);
  });

  it('shows an accessible failure and retries the same catalog request', async () => {
    mocks.getTopics
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ items: topics });
    const wrapper = mountDirectory();
    await flushPromises();

    expect(wrapper.get('[role="alert"]').text()).toBe('Topics unavailable.');
    await wrapper.get('button').trigger('click');
    await flushPromises();

    expect(mocks.getTopics).toHaveBeenCalledTimes(2);
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
    expect(wrapper.findAll('.topic-directory-view__item')).toHaveLength(2);
  });

  it('reuses the catalog loaded by RightRail', async () => {
    const rail = mountRail();
    await flushPromises();
    const directory = mountDirectory();
    await flushPromises();

    expect(mocks.getTopics).toHaveBeenCalledOnce();
    expect(rail.findAll('.right-rail__topic')).toHaveLength(2);
    expect(directory.findAll('.topic-directory-view__item')).toHaveLength(2);
  });

  it('shares initial failure without another automatic request, then updates after retry', async () => {
    mocks.getTopics
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ items: topics });
    const rail = mountRail();
    await flushPromises();
    expect(rail.get('[role="alert"]').text()).toBe('Topics unavailable.');

    const directory = mountDirectory();
    await flushPromises();
    expect(directory.get('[role="alert"]').text()).toBe('Topics unavailable.');
    expect(mocks.getTopics).toHaveBeenCalledOnce();

    await directory.get('button').trigger('click');
    await flushPromises();
    expect(mocks.getTopics).toHaveBeenCalledTimes(2);
    expect(directory.findAll('.topic-directory-view__item')).toHaveLength(2);
    expect(rail.findAll('.right-rail__topic')).toHaveLength(2);
  });
});
