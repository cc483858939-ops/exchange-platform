// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick, reactive } from 'vue';
import LeftSidebar from './LeftSidebar.vue';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  handleLogout: vi.fn(),
  route: null as any,
  homeTimeline: null as any,
}));

vi.mock('../../composables/useLogout', () => ({
  useLogout: () => ({ authStore: mocks.authStore, handleLogout: mocks.handleLogout }),
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
}));

vi.mock('../../store/homeTimeline', () => ({
  useHomeTimelineStore: () => mocks.homeTimeline,
}));

const routerLinkStub = {
  props: { to: { type: [String, Object], required: true } },
  template: '<a :data-route-name="to && to.name" :data-route-query-tab="to && to.query && to.query.tab" v-bind="$attrs"><slot /></a>',
};

const mountSidebar = () => mount(LeftSidebar, {
  global: {
    stubs: {
      AppIcon: {
        props: ['name', 'size'],
        template: '<span class="test-icon" :data-icon="name" :data-size="size" />',
      },
      RouterLink: routerLinkStub,
    },
  },
});

const dispatchClick = (
  wrapper: ReturnType<typeof mountSidebar>,
  label: string,
  init: MouseEventInit = {},
) => {
  const event = new MouseEvent('click', {
    bubbles: true,
    cancelable: true,
    button: 0,
    ...init,
  });
  wrapper.get(`.left-sidebar__nav > a[aria-label="${label}"]`).element.dispatchEvent(event);
  return event;
};

describe('LeftSidebar navigation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.authStore = reactive({
      isAuthenticated: false,
      currentIdentity: null,
    });
    mocks.route = reactive({ name: 'Home' });
    mocks.homeTimeline = reactive({
      activeTab: 'for-you' as 'for-you' | 'following',
      requestHomeReselect: vi.fn(),
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders the messenger brand mark without a duplicate wordmark', () => {
    const wrapper = mountSidebar();

    const brand = wrapper.get('.left-sidebar__brand');

    expect(brand.attributes('aria-label')).toBe('Exchange home');
    expect(brand.attributes('title')).toBe('Exchange');
    expect(brand.attributes('data-route-name')).toBe('Home');
    expect(brand.attributes('data-route-query-tab')).toBeUndefined();
    const brandEvent = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 });
    brand.element.dispatchEvent(brandEvent);
    expect(brandEvent.defaultPrevented).toBe(false);
    expect(mocks.homeTimeline.requestHomeReselect).not.toHaveBeenCalled();
    const mark = wrapper.get('.left-sidebar__brand-mark img');
    expect(mark.attributes('src')).toBe('/favicon.svg');
    expect(mark.attributes('alt')).toBe('');
    expect(mark.attributes('aria-hidden')).toBe('true');
    expect(wrapper.find('.left-sidebar__brand-name').exists()).toBe(false);
    expect(wrapper.text()).not.toContain('GX');
    expect(wrapper.text()).not.toContain('Go Exchange');
  });

  it('hides History when signed out', () => {
    const wrapper = mountSidebar();
    expect(wrapper.text()).not.toContain('History');
    expect(wrapper.find('[data-icon="history"]').exists()).toBe(false);
  });

  it('reselects active Home For You through the shared Home intent', () => {
    mocks.authStore.isAuthenticated = true;
    mocks.authStore.currentIdentity = { id: 7, username: 'reader' };
    const wrapper = mountSidebar();
    const event = dispatchClick(wrapper, 'Home');

    expect(event.defaultPrevented).toBe(true);
    expect(mocks.homeTimeline.requestHomeReselect).toHaveBeenCalledTimes(1);
  });

  it('reselects active Home Following while preserving the tab destination', () => {
    mocks.authStore.isAuthenticated = true;
    mocks.authStore.currentIdentity = { id: 7, username: 'reader' };
    mocks.homeTimeline.activeTab = 'following';
    const wrapper = mountSidebar();
    const home = wrapper.get('.left-sidebar__nav > a[aria-label="Home"]');
    const event = dispatchClick(wrapper, 'Home');

    expect(home.attributes('data-route-name')).toBe('Home');
    expect(home.attributes('data-route-query-tab')).toBe('following');
    expect(event.defaultPrevented).toBe(true);
    expect(mocks.homeTimeline.requestHomeReselect).toHaveBeenCalledTimes(1);
    expect(mocks.homeTimeline.activeTab).toBe('following');
  });

  it('navigates to Home Following from another route without reselecting', () => {
    mocks.authStore.isAuthenticated = true;
    mocks.authStore.currentIdentity = { id: 7, username: 'reader' };
    mocks.route.name = 'UserSearch';
    mocks.homeTimeline.activeTab = 'following';
    const wrapper = mountSidebar();
    const home = wrapper.get('.left-sidebar__nav > a[aria-label="Home"]');
    const event = dispatchClick(wrapper, 'Home');

    expect(event.defaultPrevented).toBe(false);
    expect(mocks.homeTimeline.requestHomeReselect).not.toHaveBeenCalled();
    expect(home.attributes('data-route-name')).toBe('Home');
    expect(home.attributes('data-route-query-tab')).toBe('following');
  });

  it('uses the canonical Home destination for the For You tab', () => {
    const wrapper = mountSidebar();
    const home = wrapper.get('.left-sidebar__nav > a[aria-label="Home"]');

    expect(home.attributes('data-route-name')).toBe('Home');
    expect(home.attributes('data-route-query-tab')).toBeUndefined();
  });

  it.each([
    ['Meta', { metaKey: true }],
    ['Ctrl', { ctrlKey: true }],
    ['Shift', { shiftKey: true }],
    ['Alt', { altKey: true }],
    ['middle', { button: 1 }],
  ])('preserves %s-click semantics for active Home', (_label, init) => {
    mocks.authStore.isAuthenticated = true;
    mocks.authStore.currentIdentity = { id: 7, username: 'reader' };
    const wrapper = mountSidebar();
    const event = dispatchClick(wrapper, 'Home', init);

    expect(event.defaultPrevented).toBe(false);
    expect(mocks.homeTimeline.requestHomeReselect).not.toHaveBeenCalled();
  });

  it.each(['Search', 'Notifications', 'History', 'Exchange', 'Profile', 'Post'])(
    'does not intercept the %s navigation item',
    (label) => {
      mocks.authStore.isAuthenticated = true;
      mocks.authStore.currentIdentity = { id: 7, username: 'reader' };
      const wrapper = mountSidebar();
      const event = dispatchClick(wrapper, label);

      expect(event.defaultPrevented).toBe(false);
      expect(mocks.homeTimeline.requestHomeReselect).not.toHaveBeenCalled();
    },
  );

  it('places authenticated History after Search and before Profile and Post', async () => {
    const wrapper = mountSidebar();
    mocks.authStore.isAuthenticated = true;
    mocks.authStore.currentIdentity = { id: 7, username: 'reader' };
    await nextTick();

    const labels = wrapper.findAll('.left-sidebar__nav > a').map(link => link.text().trim());
    const searchIndex = labels.indexOf('Search');
    const historyIndex = labels.indexOf('History');
    const profileIndex = labels.indexOf('Profile');
    const postIndex = labels.indexOf('Post');
    expect(historyIndex).toBeGreaterThan(searchIndex);
    expect(historyIndex).toBeLessThan(profileIndex);
    expect(profileIndex).toBeLessThan(postIndex);
    expect(wrapper.find('[data-icon="history"]').exists()).toBe(true);
  });

  it('uses the stronger desktop navigation scale for every authenticated action', async () => {
    const wrapper = mountSidebar();
    mocks.authStore.isAuthenticated = true;
    mocks.authStore.currentIdentity = { id: 7, username: 'reader' };
    await nextTick();

    const iconSize = (name: string) => wrapper.get(`[data-icon="${name}"]`).attributes('data-size');

    expect(iconSize('home')).toBe('26');
    expect(iconSize('search')).toBe('26');
    expect(iconSize('notifications')).toBe('26');
    expect(iconSize('history')).toBe('26');
    expect(iconSize('profile')).toBe('26');
    expect(iconSize('compose')).toBe('24');
    expect(iconSize('logout')).toBe('26');
  });
});
