// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest';
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
}));

vi.mock('../../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../../store/homeTimeline', () => ({
  useHomeTimelineStore: () => mocks.homeTimeline,
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
}));

const routerLinkStub = {
  props: { to: { type: [String, Object], required: true } },
  template: '<a :data-route-name="to && to.name" :data-route-id="to && to.params && to.params.id" :data-route-query-tab="to && to.query && to.query.tab" v-bind="$attrs"><slot /></a>',
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
