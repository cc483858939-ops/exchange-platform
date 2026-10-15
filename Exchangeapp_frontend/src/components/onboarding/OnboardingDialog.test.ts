// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  onboarding: null as any,
  homeTimeline: null as any,
  route: null as any,
  next: vi.fn(),
  skip: vi.fn(),
}));

vi.mock('../../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../../store/homeTimeline', () => ({ useHomeTimelineStore: () => mocks.homeTimeline }));
vi.mock('../../store/onboarding', () => ({ useOnboardingStore: () => mocks.onboarding }));
vi.mock('vue-router', () => ({ useRoute: () => mocks.route }));

vi.mock('./OnboardingAvatarStep.vue', async () => {
  const { defineComponent, h } = await import('vue');
  return { default: defineComponent({ name: 'OnboardingAvatarStepStub', setup: (_props, { expose }) => {
    expose({ next: mocks.next, skip: mocks.skip, canAdvance: true });
    return () => h('div', { 'data-onboarding-step': 'avatar' });
  } }) };
});
vi.mock('./OnboardingDisplayNameStep.vue', async () => {
  const { defineComponent, h } = await import('vue');
  return { default: defineComponent({ name: 'OnboardingDisplayNameStepStub', setup: (_props, { expose }) => {
    expose({ next: mocks.next, skip: mocks.skip, canAdvance: true });
    return () => h('div', { 'data-onboarding-step': 'display-name' });
  } }) };
});
vi.mock('./OnboardingInterestsStep.vue', async () => {
  const { defineComponent, h } = await import('vue');
  return { default: defineComponent({ name: 'OnboardingInterestsStepStub', setup: (_props, { expose }) => {
    expose({ next: mocks.next, skip: mocks.skip, canAdvance: true });
    return () => h('div', { 'data-onboarding-step': 'interests' });
  } }) };
});
vi.mock('./OnboardingFollowStep.vue', async () => {
  const { defineComponent, h } = await import('vue');
  return { default: defineComponent({ name: 'OnboardingFollowStepStub', setup: (_props, { expose }) => {
    expose({ next: mocks.next, skip: mocks.skip, canAdvance: true });
    return () => h('div', { 'data-onboarding-step': 'follow-users' });
  } }) };
});

import OnboardingDialog from './OnboardingDialog.vue';

const incompleteStatus = () => ({
  completed: false,
  current_step: 'avatar',
  interest_slugs: [],
  completed_at: null,
});

beforeEach(() => {
  mocks.authStore = reactive({
    isAuthenticated: true,
    currentIdentity: { id: 7, username: 'viewer7' },
    sessionVersion: 1,
  });
  mocks.onboarding = reactive({
    status: incompleteStatus(),
    loading: false,
    saving: false,
    error: '',
    temporaryDismissAvailable: false,
    currentViewerID: 7,
    isOpen: true,
    observeSession: vi.fn(),
    complete: vi.fn(async () => false),
    retry: vi.fn(),
    dismissTemporarily: vi.fn(),
  });
  mocks.homeTimeline = reactive({
    activeTab: 'for-you',
    refreshForYouAfterOnboarding: vi.fn(),
  });
  mocks.route = reactive({ name: 'Home', query: {} });
  mocks.next.mockReset();
  mocks.skip.mockReset();
});

afterEach(() => {
  document.body.innerHTML = '';
});

describe('OnboardingDialog', () => {
  it('opens a titled modal for an incomplete session and keeps setup controls within the dialog', async () => {
    const wrapper = mount(OnboardingDialog, { props: { enabled: true }, attachTo: document.body });
    await flushPromises();

    const dialog = document.body.querySelector('dialog')!;
    expect(dialog.hasAttribute('open')).toBe(true);
    expect(document.body.querySelector('#onboarding-title')?.textContent).toBe('Choose a profile picture');
    expect(document.body.querySelector('[data-onboarding-step]')).not.toBeNull();
    expect(document.body.querySelector('[role="progressbar"]')?.getAttribute('aria-valuenow')).toBe('1');
    expect(mocks.onboarding.observeSession).toHaveBeenCalledWith(true);

    await document.body.querySelector<HTMLButtonElement>('.onboarding-dialog__next')!.click();
    await wrapper.vm.$nextTick();
    expect(mocks.next).toHaveBeenCalledOnce();
    await document.body.querySelector<HTMLButtonElement>('.onboarding-dialog__skip-step')!.click();
    await wrapper.vm.$nextTick();
    expect(mocks.skip).toHaveBeenCalledOnce();
    wrapper.unmount();
  });

  it('uses server completion confirmation for Skip setup and offers temporary dismissal on failure', async () => {
    const wrapper = mount(OnboardingDialog, { props: { enabled: true }, attachTo: document.body });
    await flushPromises();
    await document.body.querySelector<HTMLButtonElement>('.onboarding-dialog__skip-setup')!.click();
    expect(mocks.onboarding.complete).toHaveBeenCalledOnce();

    mocks.onboarding.error = 'Could not complete setup.';
    mocks.onboarding.temporaryDismissAvailable = true;
    await wrapper.vm.$nextTick();
    expect(document.body.textContent).toContain('Continue to app for now');
    await document.body.querySelector<HTMLButtonElement>('.onboarding-dialog__continue')!.click();
    expect(mocks.onboarding.dismissTemporarily).toHaveBeenCalledOnce();
    wrapper.unmount();
  });

  it('refreshes For You only after confirmed completion with interests on Home', async () => {
    const wrapper = mount(OnboardingDialog, { props: { enabled: true }, attachTo: document.body });
    await flushPromises();

    mocks.onboarding.status = { ...incompleteStatus(), completed: true, interest_slugs: ['ai'], completed_at: '2026-10-15T00:00:00Z' };
    await wrapper.vm.$nextTick();
    expect(mocks.homeTimeline.refreshForYouAfterOnboarding).toHaveBeenCalledOnce();
    expect(mocks.homeTimeline.refreshForYouAfterOnboarding).toHaveBeenCalledWith(7);

    mocks.homeTimeline.refreshForYouAfterOnboarding.mockClear();
    mocks.onboarding.status = incompleteStatus();
    mocks.route.name = 'CurrencyExchange';
    mocks.onboarding.status = { ...incompleteStatus(), completed: true, interest_slugs: ['space'], completed_at: '2026-10-15T00:00:00Z' };
    await wrapper.vm.$nextTick();
    expect(mocks.homeTimeline.refreshForYouAfterOnboarding).not.toHaveBeenCalled();

    mocks.route.name = 'Home';
    mocks.homeTimeline.activeTab = 'for-you';
    mocks.onboarding.status = incompleteStatus();
    mocks.onboarding.status = { ...incompleteStatus(), completed: true, interest_slugs: [], completed_at: '2026-10-15T00:00:00Z' };
    await wrapper.vm.$nextTick();
    expect(mocks.homeTimeline.refreshForYouAfterOnboarding).not.toHaveBeenCalled();

    mocks.onboarding.status = incompleteStatus();
    mocks.route.query = { tab: 'following' };
    mocks.homeTimeline.activeTab = 'following';
    mocks.onboarding.status = { ...incompleteStatus(), completed: true, interest_slugs: ['ai'], completed_at: '2026-10-15T00:00:00Z' };
    await wrapper.vm.$nextTick();
    expect(mocks.homeTimeline.refreshForYouAfterOnboarding).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('does not display the dialog when the server status is complete or the route uses the auth layout', async () => {
    mocks.onboarding.status = { ...incompleteStatus(), completed: true, completed_at: '2026-10-15T00:00:00Z' };
    mocks.onboarding.isOpen = false;
    const wrapper = mount(OnboardingDialog, { props: { enabled: true }, attachTo: document.body });
    await flushPromises();
    expect(document.body.querySelector('dialog')?.hasAttribute('open')).toBe(false);

    await wrapper.setProps({ enabled: false });
    expect(mocks.onboarding.observeSession).toHaveBeenLastCalledWith(false);
    wrapper.unmount();
  });
});
