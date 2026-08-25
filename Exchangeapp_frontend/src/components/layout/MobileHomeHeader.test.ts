// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick, reactive } from 'vue';
import MobileHomeHeader from './MobileHomeHeader.vue';

type Identity = {
  id: number;
  username: string;
  display_name: string;
  avatar_url: string;
};

type AuthState = {
  isAuthenticated: boolean;
  currentIdentity: Identity | null;
};

const mocks = vi.hoisted(() => ({
  authStore: null as AuthState | null,
}));

vi.mock('../../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

const routerLinkStub = {
  props: { to: { type: [String, Object], required: true } },
  template: '<a :data-route-name="to && to.name" :data-route-id="to && to.params && to.params.id"><slot /></a>',
};

const mountHeader = () => mount(MobileHomeHeader, {
  global: {
    stubs: {
      RouterLink: routerLinkStub,
      AppIcon: { props: ['name'], template: '<span class="test-icon" :data-icon="name" />' },
    },
  },
});

const setAuthenticated = (identity: Identity | null) => {
  mocks.authStore = reactive({
    isAuthenticated: identity !== null,
    currentIdentity: identity,
  });
};

const identity = (overrides: Partial<Identity> = {}): Identity => ({
  id: 123,
  username: 'alice123',
  display_name: 'Alice',
  avatar_url: '',
  ...overrides,
});

describe('MobileHomeHeader', () => {
  beforeEach(() => {
    setAuthenticated(identity());
  });

  it('renders EX as a Home link', () => {
    const wrapper = mountHeader();
    const brand = wrapper.find('.mobile-home-header__brand');

    expect(brand.text()).toBe('EX');
    expect(brand.attributes('data-route-name')).toBe('Home');
  });

  it('renders the current user avatar and links to the own profile', () => {
    setAuthenticated(identity({ avatar_url: '/avatar.webp' }));
    const wrapper = mountHeader();
    const profile = wrapper.find('.mobile-home-header__profile');
    const image = wrapper.find('.user-avatar__image');

    expect(image.attributes('src')).toBe('/avatar.webp');
    expect(profile.attributes('data-route-name')).toBe('UserProfile');
    expect(profile.attributes('data-route-id')).toBe('123');
  });

  it('keeps the initial visible until the avatar image has loaded', async () => {
    setAuthenticated(identity({ avatar_url: '/avatar.webp' }));
    const wrapper = mountHeader();
    const image = wrapper.find('.user-avatar__image');

    expect(wrapper.find('.user-avatar__fallback').text()).toBe('A');
    expect(image.classes()).not.toContain('user-avatar__image--loaded');

    await image.trigger('load');
    await nextTick();
    expect(image.classes()).toContain('user-avatar__image--loaded');
  });

  it('falls back to the display name initial when the avatar fails', async () => {
    setAuthenticated(identity({ avatar_url: '/avatar.webp' }));
    const wrapper = mountHeader();
    await wrapper.find('.user-avatar__image').trigger('error');

    expect(wrapper.find('.user-avatar__image').exists()).toBe(false);
    expect(wrapper.find('.mobile-home-header__avatar').text()).toBe('A');
  });

  it('uses username and then the profile icon as fallback sources', async () => {
    const wrapper = mountHeader();

    mocks.authStore!.currentIdentity = identity({ display_name: '', username: 'bob' });
    await nextTick();
    expect(wrapper.find('.mobile-home-header__avatar').text()).toBe('B');

    mocks.authStore!.currentIdentity = identity({ display_name: '', username: '' });
    await nextTick();
    expect(wrapper.find('.user-avatar__fallback').text()).toBe('?');
  });

  it('renders the anonymous login affordance without fake actions', () => {
    setAuthenticated(null);
    const wrapper = mountHeader();
    const links = wrapper.findAll('a');

    expect(links).toHaveLength(2);
    expect(links[0].attributes('data-route-name')).toBe('Login');
    expect(links[1].attributes('data-route-name')).toBe('Home');
    expect(wrapper.findAll('button')).toHaveLength(0);
    expect(wrapper.find('.mobile-home-header__spacer').exists()).toBe(true);
  });
});
