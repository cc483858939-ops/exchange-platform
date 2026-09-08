// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick, reactive } from 'vue';
import LeftSidebar from './LeftSidebar.vue';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  handleLogout: vi.fn(),
}));

vi.mock('../../composables/useLogout', () => ({
  useLogout: () => ({ authStore: mocks.authStore, handleLogout: mocks.handleLogout }),
}));

const mountSidebar = () => mount(LeftSidebar, {
  global: {
    stubs: {
      AppIcon: { props: ['name'], template: '<span class="test-icon" :data-icon="name" />' },
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

describe('LeftSidebar navigation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.authStore = reactive({
      isAuthenticated: false,
      currentIdentity: null,
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
});
