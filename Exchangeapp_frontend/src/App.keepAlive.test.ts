// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App.vue';

const mocks = vi.hoisted(() => ({
  initializePostViewTelemetry: vi.fn(),
}));

vi.mock('./services/postViewTelemetry', () => ({
  initializePostViewTelemetry: mocks.initializePostViewTelemetry,
}));

vi.mock('./store/auth', () => ({
  useAuthStore: () => ({
    currentIdentity: null,
  }),
}));

const HomeProbe = defineComponent({
  name: 'HomeView',
  template: '<main data-home-marker><img data-home-image src="/avatar.webp" /></main>',
});

const PostDetailProbe = defineComponent({
  name: 'PostDetailView',
  template: '<main data-detail-marker>Detail</main>',
});

const SearchProbe = defineComponent({
  name: 'UserSearchView',
  template: '<main data-search-marker>Search</main>',
});

const createTestRouter = () => createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/', name: 'Home', component: HomeProbe, meta: { layout: 'app' } },
    { path: '/posts/:id', name: 'PostDetail', component: PostDetailProbe, meta: { layout: 'app' } },
    { path: '/search', name: 'UserSearch', component: SearchProbe, meta: { layout: 'app' } },
  ],
});

const settle = async () => {
  await flushPromises();
  await flushPromises();
};

const mountApp = async () => {
  const router = createTestRouter();
  await router.push('/');
  await router.isReady();
  const wrapper = mount(App, {
    global: {
      plugins: [router],
      stubs: {
        AppShell: { template: '<div data-app-shell><slot /></div>' },
      },
    },
  });
  await settle();
  return { router, wrapper };
};

describe('App Home cache scope', () => {
  beforeEach(() => {
    mocks.initializePostViewTelemetry.mockClear();
  });

  it('preserves the Home DOM identity across PostDetail navigation', async () => {
    const { router, wrapper } = await mountApp();
    const originalMarker = wrapper.find('[data-home-marker]').element;
    const originalImage = wrapper.find('[data-home-image]').element;

    await router.push('/posts/42');
    await settle();
    expect(wrapper.find('[data-detail-marker]').exists()).toBe(true);

    await router.push('/');
    await settle();

    expect(wrapper.find('[data-home-marker]').element).toBe(originalMarker);
    expect(wrapper.find('[data-home-image]').element).toBe(originalImage);
    wrapper.unmount();
  });

  it('does not keep Home cached when navigating through another app route', async () => {
    const { router, wrapper } = await mountApp();
    const originalMarker = wrapper.find('[data-home-marker]').element;

    await router.push('/search');
    await settle();
    expect(wrapper.find('[data-search-marker]').exists()).toBe(true);

    await router.push('/');
    await settle();

    expect(wrapper.find('[data-home-marker]').element).not.toBe(originalMarker);
    wrapper.unmount();
  });
});
