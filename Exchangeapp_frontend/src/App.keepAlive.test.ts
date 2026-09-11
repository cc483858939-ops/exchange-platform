// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, reactive } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App.vue';

const mocks = vi.hoisted(() => ({
  initializePostViewTelemetry: vi.fn(),
  authStore: null as any,
}));

vi.mock('./services/postViewTelemetry', () => ({
  initializePostViewTelemetry: mocks.initializePostViewTelemetry,
}));

vi.mock('./store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

const HomeProbe = defineComponent({
  name: 'HomeView',
  template: '<main data-home-marker><img data-home-image src="/avatar.webp" /></main>',
});

const ExchangeProbe = defineComponent({
  name: 'LiveExchangeView',
  template: '<main data-exchange-marker>Exchange</main>',
});

const NotificationsProbe = defineComponent({
  name: 'NotificationsView',
  template: '<main data-notifications-marker>Notifications</main>',
});

const PostDetailProbe = defineComponent({
  name: 'PostDetailView',
  template: '<main data-detail-marker>Detail</main>',
});

const HistoryProbe = defineComponent({
  name: 'HistoryView',
  template: `
    <main data-history-marker>
      <article data-history-post>
        <img data-history-image src="/history.webp" />
      </article>
    </main>
  `,
});

const UserProfileProbe = defineComponent({
  name: 'UserProfileView',
  template: `
    <main data-profile-marker>
      <article data-profile-post>
        <img data-profile-image src="/profile-post.webp" />
      </article>
    </main>
  `,
});

const SearchProbe = defineComponent({
  name: 'UserSearchView',
  template: '<main data-search-marker>Search</main>',
});

const TransientProbe = defineComponent({
  name: 'TransientProbe',
  template: '<main data-transient-marker>Transient</main>',
});

const createTestRouter = () => createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/', name: 'Home', component: HomeProbe, meta: { layout: 'app' } },
    { path: '/exchange', name: 'CurrencyExchange', component: ExchangeProbe, meta: { layout: 'app' } },
    { path: '/notifications', name: 'Notifications', component: NotificationsProbe, meta: { layout: 'app' } },
    { path: '/posts/:id', name: 'PostDetail', component: PostDetailProbe, meta: { layout: 'app' } },
    { path: '/users/:id', name: 'UserProfile', component: UserProfileProbe, meta: { layout: 'app' } },
    { path: '/users/:id/following', name: 'UserFollowing', component: TransientProbe, meta: { layout: 'app' } },
    { path: '/users/:id/followers', name: 'UserFollowers', component: TransientProbe, meta: { layout: 'app' } },
    { path: '/search', name: 'UserSearch', component: SearchProbe, meta: { layout: 'app' } },
    { path: '/history', name: 'History', component: HistoryProbe, meta: { layout: 'app' } },
  ],
});

const settle = async () => {
  await flushPromises();
  await flushPromises();
};

const mountApp = async (initialPath = '/') => {
  const router = createTestRouter();
  await router.push(initialPath);
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

describe('App root and external surface caches', () => {
  beforeEach(() => {
    mocks.initializePostViewTelemetry.mockClear();
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: { id: 7, username: 'viewer-7' },
    });
  });

  it.each([
    ['Search', '/search', '[data-search-marker]'],
    ['Exchange', '/exchange', '[data-exchange-marker]'],
    ['Notifications', '/notifications', '[data-notifications-marker]'],
    ['Own Profile', '/users/7', '[data-profile-marker]'],
  ])('preserves Home DOM identity across Home → %s → Home', async (_label, path, marker) => {
    const { router, wrapper } = await mountApp();
    const originalHome = wrapper.find('[data-home-marker]').element;
    const originalImage = wrapper.find('[data-home-image]').element;

    await router.push(path);
    await settle();
    expect(wrapper.find(marker).exists()).toBe(true);

    await router.push('/');
    await settle();

    expect(wrapper.find('[data-home-marker]').element).toBe(originalHome);
    expect(wrapper.find('[data-home-image]').element).toBe(originalImage);
    wrapper.unmount();
  });

  it('preserves own Profile DOM identity across Home navigation', async () => {
    const { router, wrapper } = await mountApp('/users/7');
    const originalProfile = wrapper.find('[data-profile-marker]').element;
    const originalPost = wrapper.find('[data-profile-post]').element;
    const originalImage = wrapper.find('[data-profile-image]').element;

    await router.push('/');
    await settle();
    await router.push('/users/7');
    await settle();

    expect(wrapper.find('[data-profile-marker]').element).toBe(originalProfile);
    expect(wrapper.find('[data-profile-post]').element).toBe(originalPost);
    expect(wrapper.find('[data-profile-image]').element).toBe(originalImage);
    wrapper.unmount();
  });

  it('keeps the same Search DOM when only its query changes', async () => {
    const { router, wrapper } = await mountApp('/search?q=alice');
    const originalSearch = wrapper.find('[data-search-marker]').element;

    await router.push('/search?q=bob');
    await settle();

    expect(wrapper.find('[data-search-marker]').element).toBe(originalSearch);
    wrapper.unmount();
  });

  it('keeps an external Profile through PostDetail and restores its DOM', async () => {
    const { router, wrapper } = await mountApp('/users/8');
    const originalProfile = wrapper.find('[data-profile-marker]').element;
    const originalPost = wrapper.find('[data-profile-post]').element;
    const originalImage = wrapper.find('[data-profile-image]').element;

    await router.push('/posts/42');
    await settle();
    expect(wrapper.find('[data-detail-marker]').exists()).toBe(true);

    await router.push('/users/8');
    await settle();

    expect(wrapper.find('[data-profile-marker]').element).toBe(originalProfile);
    expect(wrapper.find('[data-profile-post]').element).toBe(originalPost);
    expect(wrapper.find('[data-profile-image]').element).toBe(originalImage);
    wrapper.unmount();
  });

  it('separates own and external Profile caches', async () => {
    const { router, wrapper } = await mountApp('/users/7');
    const ownProfile = wrapper.find('[data-profile-marker]').element;

    await router.push('/users/8');
    await settle();
    const externalProfile = wrapper.find('[data-profile-marker]').element;
    expect(externalProfile).not.toBe(ownProfile);

    await router.push('/users/7');
    await settle();

    expect(wrapper.find('[data-profile-marker]').element).toBe(ownProfile);
    wrapper.unmount();
  });

  it('releases the external Profile cache when entering own Profile', async () => {
    const { router, wrapper } = await mountApp('/users/8');
    const firstExternalProfile = wrapper.find('[data-profile-marker]').element;

    await router.push('/users/7');
    await settle();
    expect(wrapper.find('[data-profile-marker]').element).not.toBe(firstExternalProfile);

    await router.push('/users/8');
    await settle();

    expect(wrapper.find('[data-profile-marker]').element).not.toBe(firstExternalProfile);
    wrapper.unmount();
  });

  it('keeps an external Profile through UserFollowing', async () => {
    const { router, wrapper } = await mountApp('/users/8');
    const originalProfile = wrapper.find('[data-profile-marker]').element;

    await router.push('/users/8/following');
    await settle();
    expect(wrapper.find('[data-transient-marker]').exists()).toBe(true);

    await router.push('/users/8');
    await settle();

    expect(wrapper.find('[data-profile-marker]').element).toBe(originalProfile);
    wrapper.unmount();
  });

  it('preserves History DOM through PostDetail and restores its presentation', async () => {
    const { router, wrapper } = await mountApp('/history');
    const originalHistory = wrapper.find('[data-history-marker]').element;
    const originalPost = wrapper.find('[data-history-post]').element;
    const originalImage = wrapper.find('[data-history-image]').element;

    await router.push('/posts/42');
    await settle();
    expect(wrapper.find('[data-detail-marker]').exists()).toBe(true);

    await router.push('/history');
    await settle();

    expect(wrapper.find('[data-history-marker]').element).toBe(originalHistory);
    expect(wrapper.find('[data-history-post]').element).toBe(originalPost);
    expect(wrapper.find('[data-history-image]').element).toBe(originalImage);
    wrapper.unmount();
  });

  it('releases the History cache when navigating to Home', async () => {
    const { router, wrapper } = await mountApp('/history');
    const originalHistory = wrapper.find('[data-history-marker]').element;

    await router.push('/');
    await settle();
    await router.push('/history');
    await settle();

    expect(wrapper.find('[data-history-marker]').element).not.toBe(originalHistory);
    wrapper.unmount();
  });

  it('isolates the History cache by viewer namespace', async () => {
    const { router, wrapper } = await mountApp('/history');
    const viewerSevenHistory = wrapper.find('[data-history-marker]').element;

    await router.push('/posts/42');
    await settle();
    mocks.authStore.currentIdentity = { id: 8, username: 'viewer-8' };
    await settle();
    await router.push('/history');
    await settle();

    expect(wrapper.find('[data-history-marker]').element).not.toBe(viewerSevenHistory);
    wrapper.unmount();
  });

  it('limits the external Profile cache to one entry', async () => {
    const { router, wrapper } = await mountApp('/users/8');
    const firstProfile = wrapper.find('[data-profile-marker]').element;

    await router.push('/users/9');
    await settle();
    expect(wrapper.find('[data-profile-marker]').element).not.toBe(firstProfile);

    await router.push('/users/8');
    await settle();

    expect(wrapper.find('[data-profile-marker]').element).not.toBe(firstProfile);
    wrapper.unmount();
  });

  it('releases the external Profile cache on root navigation', async () => {
    const { router, wrapper } = await mountApp('/users/8');
    const firstProfile = wrapper.find('[data-profile-marker]').element;

    await router.push('/');
    await settle();
    await router.push('/users/8');
    await settle();

    expect(wrapper.find('[data-profile-marker]').element).not.toBe(firstProfile);
    wrapper.unmount();
  });

  it('destroys root DOM caches when the viewer namespace changes', async () => {
    const { router, wrapper } = await mountApp();
    const firstHome = wrapper.find('[data-home-marker]').element;

    await router.push('/search');
    await settle();
    mocks.authStore.currentIdentity = { id: 8, username: 'viewer-8' };
    await settle();
    await router.push('/');
    await settle();

    expect(wrapper.find('[data-home-marker]').element).not.toBe(firstHome);
    wrapper.unmount();
  });

  it('retains all five root surfaces within the root cache capacity', async () => {
    const { router, wrapper } = await mountApp('/');
    const original = new Map([
      ['Home', wrapper.find('[data-home-marker]').element],
    ]);

    await router.push('/search');
    await settle();
    original.set('Search', wrapper.find('[data-search-marker]').element);
    await router.push('/exchange');
    await settle();
    original.set('Exchange', wrapper.find('[data-exchange-marker]').element);
    await router.push('/notifications');
    await settle();
    original.set('Notifications', wrapper.find('[data-notifications-marker]').element);
    await router.push('/users/7');
    await settle();
    original.set('Profile', wrapper.find('[data-profile-marker]').element);

    for (const [path, selector, key] of [
      ['/', '[data-home-marker]', 'Home'],
      ['/search', '[data-search-marker]', 'Search'],
      ['/exchange', '[data-exchange-marker]', 'Exchange'],
      ['/notifications', '[data-notifications-marker]', 'Notifications'],
      ['/users/7', '[data-profile-marker]', 'Profile'],
    ] as const) {
      await router.push(path);
      await settle();
      expect(wrapper.find(selector).element).toBe(original.get(key));
    }
    wrapper.unmount();
  });
});
