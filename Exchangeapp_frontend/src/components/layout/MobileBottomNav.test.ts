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
};

const mocks = vi.hoisted(() => ({
  authStore: null as AuthState | null,
  route: null as RouteState | null,
  homeTimeline: { activeTab: 'for-you' as 'for-you' | 'following' },
  searchSession: { query: '' },
}));

vi.mock('../../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../../store/homeTimeline', () => ({
  useHomeTimelineStore: () => mocks.homeTimeline,
}));

vi.mock('../../store/searchSession', () => ({
  useSearchSessionStore: () => mocks.searchSession,
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
}));

const routerLinkStub = {
  props: { to: { type: [String, Object], required: true } },
  template: '<a :data-route-name="to && to.name" :data-route-id="to && to.params && to.params.id" :data-route-query-tab="to && to.query && to.query.tab" :data-route-query-q="to && to.query && to.query.q" v-bind="$attrs"><slot /></a>',
};

const mountNav = (notificationBadge: string | null = null) => mount(MobileBottomNav, {
  props: { notificationBadge },
  global: {
    stubs: {
      RouterLink: routerLinkStub,
      AppIcon: { props: ['name'], template: '<span class="test-icon" :data-icon="name" />' },
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
  mocks.route = reactive({ name: routeName, params });
};

describe('MobileBottomNav', () => {
  beforeEach(() => {
    setState(true);
    mocks.homeTimeline.activeTab = 'for-you';
    mocks.searchSession.query = '';
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

  it('renders only Home, Exchange, and Log in anonymously', () => {
    setState(false);
    const wrapper = mountNav();
    const labels = wrapper.findAll('.mobile-bottom-nav__item').map(item => item.text().trim());

    expect(labels).toEqual(['Home', 'Exchange', 'Log in']);
    expect(wrapper.text()).not.toContain('Search');
    expect(wrapper.text()).not.toContain('Notifications');
    expect(wrapper.text()).not.toContain('Profile');
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

  it('reselects the active Home root to the top without changing For You', () => {
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 0);

    expect(event.defaultPrevented).toBe(true);
    expect(window.scrollTo).toHaveBeenCalledTimes(1);
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
    expect(mocks.homeTimeline.activeTab).toBe('for-you');
  });

  it('reselects Home while preserving the Following tab', () => {
    mocks.homeTimeline.activeTab = 'following';
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 0);

    expect(event.defaultPrevented).toBe(true);
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
    expect(mocks.homeTimeline.activeTab).toBe('following');
    expect(wrapper.find('.mobile-bottom-nav__item').attributes('data-route-query-tab')).toBe('following');
  });

  it('reselects Search without changing the active query', () => {
    setState(true, 'UserSearch');
    mocks.searchSession.query = 'alice';
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 1);

    expect(event.defaultPrevented).toBe(true);
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
    expect(mocks.searchSession.query).toBe('alice');
    expect(wrapper.findAll('.mobile-bottom-nav__item')[1].attributes('data-route-query-q')).toBe('alice');
  });

  it.each([
    ['CurrencyExchange', 2],
    ['Notifications', 3],
  ])('reselects the %s root to the top', (routeName, index) => {
    setState(true, routeName);
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, index);

    expect(event.defaultPrevented).toBe(true);
    expect(window.scrollTo).toHaveBeenCalledTimes(1);
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
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
    expect(window.scrollTo).not.toHaveBeenCalled();
  });

  it('uses auto scrolling when reduced motion is preferred', () => {
    setState(true, 'Home');
    const matchMedia = window.matchMedia as unknown as ReturnType<typeof vi.fn>;
    matchMedia.mockImplementation((query: string) => ({ matches: query.includes('prefers-reduced-motion') }));
    const wrapper = mountNav();
    const event = dispatchClick(wrapper, 0);

    expect(event.defaultPrevented).toBe(true);
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'auto' });
    expect(matchMedia).toHaveBeenCalledWith('(prefers-reduced-motion: reduce)');
  });

  it('renders a store-provided badge once and omits it when null', async () => {
    const wrapper = mountNav('4');
    expect(wrapper.findAll('.mobile-bottom-nav__badge')).toHaveLength(1);
    expect(wrapper.find('.mobile-bottom-nav__badge').text()).toBe('4');

    await wrapper.setProps({ notificationBadge: null });
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
  });

  it('does not mark another user profile active', () => {
    setState(true, 'UserProfile', { id: '456' });
    const wrapper = mountNav();

    expect(wrapper.findAll('.mobile-bottom-nav__item')[4].attributes('aria-current')).toBeUndefined();
  });
});
