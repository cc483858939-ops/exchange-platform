// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { reactive } from 'vue';
import MobileBottomNav from './MobileBottomNav.vue';

type AuthState = {
  isAuthenticated: boolean;
  currentIdentity: { id: number } | null;
};

type RouteState = {
  name: string;
  params: Record<string, string | string[]>;
  query: Record<string, string | string[]>;
  fullPath?: string;
};

const mocks = vi.hoisted(() => ({
  authStore: null as AuthState | null,
  route: null as RouteState | null,
  homeTimeline: {
    activeTab: 'for-you' as 'for-you' | 'following',
    requestHomeReselect: vi.fn(),
  },
  notificationStore: {
    requestNotificationReselect: vi.fn(),
  },
  searchSession: {
    query: '',
    requestSearchReselect: vi.fn(),
  },
}));

vi.mock('../../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../../store/homeTimeline', () => ({
  useHomeTimelineStore: () => mocks.homeTimeline,
}));

vi.mock('../../store/notification', () => ({
  useNotificationStore: () => mocks.notificationStore,
}));

vi.mock('../../store/searchSession', () => ({
  useSearchSessionStore: () => mocks.searchSession,
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
}));

const routerLinkStub = {
  props: { to: { type: [String, Object], required: true } },
  template: '<a :data-route-name="to && to.name" :data-route-id="to && to.params && to.params.id" :data-route-query-tab="to && to.query && to.query.tab" :data-route-query-q="to && to.query && to.query.q" :data-route-query-return-to="to && to.query && to.query.returnTo" :data-route-query-intent="to && to.query && to.query.intent" v-bind="$attrs"><slot /></a>',
};

const mountNav = (notificationBadge: string | null = null) => mount(MobileBottomNav, {
  props: { notificationBadge },
  global: {
    stubs: {
      RouterLink: routerLinkStub,
      AppIcon: {
        props: ['name', 'size', 'filled'],
        template: '<span class="test-icon" :data-icon="name" :data-size="size" :data-filled="filled ? \'true\' : \'false\'" />',
      },
    },
  },
});

const originalMatchMedia = Object.getOwnPropertyDescriptor(window, 'matchMedia');

const dispatchClick = (
  wrapper: ReturnType<typeof mountNav>,
  index: number,
  init: MouseEventInit = {},
) => {
  const event = new MouseEvent('click', {
    bubbles: true,
    cancelable: true,
    button: 0,
    ...init,
  });
  wrapper.findAll('.mobile-bottom-nav__item')[index].element.dispatchEvent(event);
  return event;
};

const setState = (
  isAuthenticated: boolean,
  routeName = 'Home',
  params: Record<string, string | string[]> = {},
) => {
  mocks.authStore = reactive({
    isAuthenticated,
    currentIdentity: isAuthenticated ? { id: 123 } : null,
  });
  mocks.route = reactive({ name: routeName, params, query: {} });
};

describe('MobileBottomNav', () => {
  beforeEach(() => {
    setState(true);
    mocks.homeTimeline.activeTab = 'for-you';
    mocks.homeTimeline.requestHomeReselect.mockClear();
    mocks.notificationStore.requestNotificationReselect.mockClear();
    mocks.searchSession.query = '';
    mocks.searchSession.requestSearchReselect.mockClear();
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    if (originalMatchMedia) {
      Object.defineProperty(window, 'matchMedia', originalMatchMedia);
    } else {
      Reflect.deleteProperty(window, 'matchMedia');
    }
  });

  it('renders the authenticated navigation in the frozen order', () => {
    const wrapper = mountNav();
    const labels = wrapper.findAll('.mobile-bottom-nav__item').map(item => item.text().trim());

    expect(labels).toEqual(['Home', 'Search', 'Exchange', 'Notifications', 'Profile']);
  });

  it('renders the same five destinations anonymously', () => {
    setState(false);
    const wrapper = mountNav();
    const labels = wrapper.findAll('.mobile-bottom-nav__item').map(item => item.text().trim());

    expect(labels).toEqual(['Home', 'Search', 'Exchange', 'Notifications', 'Profile']);
  });

  it('gates anonymous protected destinations without fabricating a profile id', () => {
    setState(false);
    mocks.route!.query = { q: 'alice' };
    const wrapper = mountNav();
    const links = wrapper.findAll('.mobile-bottom-nav__item');

    expect(links[0].attributes('data-route-name')).toBe('Home');
    expect(links[1].attributes('data-route-name')).toBe('Login');
    expect(links[1].attributes('data-route-query-return-to')).toBe('/search?q=alice');
    expect(links[2].attributes('data-route-name')).toBe('CurrencyExchange');
    expect(links[3].attributes('data-route-name')).toBe('Login');
    expect(links[3].attributes('data-route-query-return-to')).toBe('/notifications');
    expect(links[4].attributes('data-route-name')).toBe('Login');
    expect(links[4].attributes('data-route-query-intent')).toBe('profile');
    expect(links[4].attributes('data-route-id')).toBeUndefined();
  });

  it('routes the authenticated Profile item to the current identity', () => {
    const wrapper = mountNav();
    const profile = wrapper.findAll('.mobile-bottom-nav__item')[4];

    expect(profile.attributes('data-route-name')).toBe('UserProfile');
    expect(profile.attributes('data-route-id')).toBe('123');
  });

  it('preserves the following tab in the Home destination', () => {
    mocks.homeTimeline.activeTab = 'following';
    const wrapper = mountNav();
    const home = wrapper.find('.mobile-bottom-nav__item');

    expect(home.attributes('data-route-name')).toBe('Home');
    expect(home.attributes('data-route-query-tab')).toBe('following');
  });

  it('preserves the active search query in the Search destination', () => {
    mocks.searchSession.query = 'alice';
    const wrapper = mountNav();
    const search = wrapper.findAll('.mobile-bottom-nav__item')[1];

    expect(search.attributes('data-route-name')).toBe('UserSearch');
    expect(search.attributes('data-route-query-q')).toBe('alice');
  });

  it('uses the optical icon sizes for authenticated navigation', () => {
    const wrapper = mountNav();

    expect(wrapper.findAll('.test-icon').map(icon => Number(icon.attributes('data-size')))).toEqual([
      25,
      27,
      26,
      26,
      25,
    ]);
  });

  it('uses the shared outline treatment for every authenticated icon', () => {
    for (const [routeName, params] of [
      ['Home', {}],
      ['UserSearch', {}],
      ['CurrencyExchange', {}],
      ['Notifications', {}],
      ['UserProfile', { id: '123' }],
    ] as const) {
      setState(true, routeName, params);
      const wrapper = mountNav();

      expect(wrapper.findAll('.test-icon').every(icon => icon.attributes('data-filled') !== 'true')).toBe(true);
    }
  });

  it('marks exactly one active icon capsule and keeps the other icons inactive', () => {
    setState(true, 'UserSearch');
    const wrapper = mountNav();
    const links = wrapper.findAll('.mobile-bottom-nav__item');

    expect(wrapper.findAll('.mobile-bottom-nav__icon--active')).toHaveLength(1);
    expect(links[1].find('.mobile-bottom-nav__icon').classes()).toContain('mobile-bottom-nav__icon--active');
    expect(links[0].find('.mobile-bottom-nav__icon').classes()).not.toContain('mobile-bottom-nav__icon--active');
    expect(links[2].find('.mobile-bottom-nav__icon').classes()).not.toContain('mobile-bottom-nav__icon--active');
    expect(links[3].find('.mobile-bottom-nav__icon').classes()).not.toContain('mobile-bottom-nav__icon--active');
    expect(links[4].find('.mobile-bottom-nav__icon').classes()).not.toContain('mobile-bottom-nav__icon--active');
  });

  it('keeps anonymous navigation at five items with the authenticated optical sizes', () => {
    setState(false, 'Home');
    const wrapper = mountNav();
    const icons = wrapper.findAll('.test-icon');

    expect(wrapper.findAll('.mobile-bottom-nav__item')).toHaveLength(5);
    expect(icons.map(icon => Number(icon.attributes('data-size')))).toEqual([25, 27, 26, 26, 25]);
    expect(wrapper.findAll('.mobile-bottom-nav__icon--active')).toHaveLength(1);
    expect(wrapper.findAll('.mobile-bottom-nav__item')[0].find('.mobile-bottom-nav__icon').classes())
      .toContain('mobile-bottom-nav__icon--active');
  });

  it('reselects guest active Home through the shared Home intent', () => {
    setState(false, 'Home');
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 0);

    expect(event.defaultPrevented).toBe(true);
    expect(mocks.homeTimeline.requestHomeReselect).toHaveBeenCalledTimes(1);
    expect(mocks.searchSession.requestSearchReselect).not.toHaveBeenCalled();
    expect(mocks.notificationStore.requestNotificationReselect).not.toHaveBeenCalled();
  });

  it.each([
    ['Search', 'UserSearch', 1],
    ['Notifications', 'Notifications', 3],
    ['Profile', 'UserProfile', 4],
  ] as const)('keeps guest %s on its Login destination without reselect interception', (
    _label,
    routeName,
    index,
  ) => {
    setState(false, routeName);
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, index);
    const link = wrapper.findAll('.mobile-bottom-nav__item')[index];

    expect(link.attributes('data-route-name')).toBe('Login');
    expect(event.defaultPrevented).toBe(false);
    expect(mocks.homeTimeline.requestHomeReselect).not.toHaveBeenCalled();
    expect(mocks.searchSession.requestSearchReselect).not.toHaveBeenCalled();
    expect(mocks.notificationStore.requestNotificationReselect).not.toHaveBeenCalled();
  });

  it.each([
    ['Meta', { metaKey: true }],
    ['Ctrl', { ctrlKey: true }],
    ['Shift', { shiftKey: true }],
    ['Alt', { altKey: true }],
    ['middle', { button: 1 }],
  ])('preserves guest %s-click behavior on an active Home', (_label, init) => {
    setState(false, 'Home');
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 0, init);

    expect(event.defaultPrevented).toBe(false);
    expect(mocks.homeTimeline.requestHomeReselect).not.toHaveBeenCalled();
  });

  it('signals an active Home reselect without scrolling the window or changing For You', () => {
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 0);

    expect(event.defaultPrevented).toBe(true);
    expect(mocks.homeTimeline.requestHomeReselect).toHaveBeenCalledTimes(1);
    expect(window.scrollTo).not.toHaveBeenCalled();
    expect(mocks.homeTimeline.activeTab).toBe('for-you');
  });

  it('reselects Home while preserving the Following tab', () => {
    mocks.homeTimeline.activeTab = 'following';
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 0);

    expect(event.defaultPrevented).toBe(true);
    expect(mocks.homeTimeline.requestHomeReselect).toHaveBeenCalledTimes(1);
    expect(window.scrollTo).not.toHaveBeenCalled();
    expect(mocks.homeTimeline.activeTab).toBe('following');
    expect(wrapper.find('.mobile-bottom-nav__item').attributes('data-route-query-tab')).toBe('following');
  });

  it('signals an active Search reselect without scrolling the window or changing the query', () => {
    setState(true, 'UserSearch');
    mocks.searchSession.query = 'alice';
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 1);

    expect(event.defaultPrevented).toBe(true);
    expect(mocks.searchSession.requestSearchReselect).toHaveBeenCalledTimes(1);
    expect(window.scrollTo).not.toHaveBeenCalled();
    expect(mocks.searchSession.query).toBe('alice');
    expect(wrapper.findAll('.mobile-bottom-nav__item')[1].attributes('data-route-query-q')).toBe('alice');
  });

  it.each([
    ['Meta', { metaKey: true }],
    ['Ctrl', { ctrlKey: true }],
    ['middle', { button: 1 }],
  ])('preserves %s-click link behavior on an active Search', (_label, init) => {
    setState(true, 'UserSearch');
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 1, init);

    expect(event.defaultPrevented).toBe(false);
    expect(mocks.searchSession.requestSearchReselect).not.toHaveBeenCalled();
    expect(window.scrollTo).not.toHaveBeenCalled();
  });

  it('reselects CurrencyExchange to the top', () => {
    const routeName = 'CurrencyExchange';
    const index = 2;
    setState(true, routeName);
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, index);

    expect(event.defaultPrevented).toBe(true);
    expect(window.scrollTo).toHaveBeenCalledTimes(1);
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
  });

  it('signals an active Notifications reselect without scrolling the window', () => {
    setState(true, 'Notifications');
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 3);

    expect(event.defaultPrevented).toBe(true);
    expect(mocks.notificationStore.requestNotificationReselect).toHaveBeenCalledTimes(1);
    expect(window.scrollTo).not.toHaveBeenCalled();
  });

  it('reselects the own Profile root to the top', () => {
    setState(true, 'UserProfile', { id: '123' });
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 4);

    expect(event.defaultPrevented).toBe(true);
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
  });

  it.each([
    ['History', {}],
    ['UserFollowing', { id: '123' }],
    ['UserFollowers', { id: '123' }],
  ])('navigates from the %s Profile surface without reselect scrolling', (routeName, params) => {
    setState(true, routeName, params);
    const wrapper = mountNav();
    const profile = wrapper.findAll('.mobile-bottom-nav__item')[4];
    const event = dispatchClick(wrapper, 4);

    expect(event.defaultPrevented).toBe(false);
    expect(window.scrollTo).not.toHaveBeenCalled();
    expect(profile.attributes('data-route-name')).toBe('UserProfile');
    expect(profile.attributes('data-route-id')).toBe('123');
  });

  it('navigates from another user Profile without reselect scrolling', () => {
    setState(true, 'UserProfile', { id: '456' });
    const wrapper = mountNav();
    const profile = wrapper.findAll('.mobile-bottom-nav__item')[4];
    const event = dispatchClick(wrapper, 4);

    expect(event.defaultPrevented).toBe(false);
    expect(window.scrollTo).not.toHaveBeenCalled();
    expect(profile.attributes('data-route-name')).toBe('UserProfile');
    expect(profile.attributes('data-route-id')).toBe('123');
  });

  it.each([
    ['Ctrl', { ctrlKey: true }],
    ['Meta', { metaKey: true }],
    ['Shift', { shiftKey: true }],
    ['Alt', { altKey: true }],
    ['middle', { button: 1 }],
  ])('preserves %s-click link behavior on a Home root', (_label, init) => {
    setState(true, 'Home');
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 0, init);

    expect(event.defaultPrevented).toBe(false);
    expect(mocks.homeTimeline.requestHomeReselect).not.toHaveBeenCalled();
    expect(window.scrollTo).not.toHaveBeenCalled();
  });

  it('uses auto scrolling for non-Search reselects when reduced motion is preferred', () => {
    setState(true, 'CurrencyExchange');
    const matchMedia = window.matchMedia as unknown as ReturnType<typeof vi.fn>;
    matchMedia.mockImplementation((query: string) => ({ matches: query.includes('prefers-reduced-motion') }));
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 2);

    expect(event.defaultPrevented).toBe(true);
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'auto' });
    expect(matchMedia).toHaveBeenCalledWith('(prefers-reduced-motion: reduce)');
  });

  it('renders a store-provided badge once and omits it when null', async () => {
    const wrapper = mountNav('4');
    expect(wrapper.findAll('.mobile-bottom-nav__badge')).toHaveLength(1);
    expect(wrapper.find('.mobile-bottom-nav__badge').text()).toBe('4');
    expect(wrapper.find('.mobile-bottom-nav__badge').element.parentElement?.classList.contains('mobile-bottom-nav__icon')).toBe(true);

    await wrapper.setProps({ notificationBadge: null });
    expect(wrapper.find('.mobile-bottom-nav__badge').exists()).toBe(false);
  });

  it('hides a stale notification badge while signed out', () => {
    setState(false);
    const wrapper = mountNav('4');

    expect(wrapper.find('.mobile-bottom-nav__badge').exists()).toBe(false);
  });

  it.each([
    ['Home', {}, 0],
    ['UserSearch', {}, 1],
    ['CurrencyExchange', {}, 2],
    ['Notifications', {}, 3],
    ['UserProfile', { id: '123' }, 4],
    ['UserFollowing', { id: '123' }, 4],
    ['UserFollowers', { id: ['123'] }, 4],
    ['History', {}, 4],
  ])('marks the matching %s surface active', (routeName, params, activeIndex) => {
    setState(true, routeName, params);
    const wrapper = mountNav();
    const links = wrapper.findAll('.mobile-bottom-nav__item');

    expect(links[activeIndex].attributes('aria-current')).toBe('page');
    expect(links.filter(link => link.attributes('aria-current') === 'page')).toHaveLength(1);
    expect(wrapper.findAll('.mobile-bottom-nav__icon--active')).toHaveLength(1);
    expect(links[activeIndex].find('.mobile-bottom-nav__icon').classes())
      .toContain('mobile-bottom-nav__icon--active');
  });

  it('does not mark another user profile active', () => {
    setState(true, 'UserProfile', { id: '456' });
    const wrapper = mountNav();

    expect(wrapper.findAll('.mobile-bottom-nav__item')[4].attributes('aria-current')).toBeUndefined();
  });
});
