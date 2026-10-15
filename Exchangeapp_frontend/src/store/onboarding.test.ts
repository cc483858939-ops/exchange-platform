import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => {
  const state: { binding: any; authenticated: boolean } = { binding: null, authenticated: false };
  const authStore = {
    get isAuthenticated() { return state.authenticated; },
    captureRequestAuthBinding: () => state.binding,
    matchesRequestAuthBinding: (binding: any) => Boolean(
      state.authenticated && state.binding
      && state.binding.userID === binding.userID
      && state.binding.sessionID === binding.sessionID
      && state.binding.sessionVersion === binding.sessionVersion,
    ),
  };
  return {
    state,
    authStore,
    getStatus: vi.fn(),
    saveProgress: vi.fn(),
    saveInterests: vi.fn(),
    complete: vi.fn(),
  };
});

vi.mock('./auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/onboardingService', () => ({
  getOnboardingStatus: mocks.getStatus,
  saveOnboardingProgress: mocks.saveProgress,
  saveOnboardingInterests: mocks.saveInterests,
  completeOnboarding: mocks.complete,
}));

import { useOnboardingStore } from './onboarding';
import type { OnboardingStatus } from '../services/onboardingService';

const makeStatus = (userID: number, overrides: Partial<OnboardingStatus> = {}): OnboardingStatus => ({
  completed: false,
  current_step: 'avatar',
  interest_slugs: [],
  completed_at: null,
  ...overrides,
});

const bindingFor = (userID: number) => ({ userID, sessionID: `session-${userID}`, sessionVersion: userID });

describe('onboarding store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    mocks.state.binding = null;
    mocks.state.authenticated = false;
    mocks.getStatus.mockReset();
    mocks.saveProgress.mockReset();
    mocks.saveInterests.mockReset();
    mocks.complete.mockReset();
  });

  it('loads once per authenticated session and does not query while logged out', async () => {
    const store = useOnboardingStore();
    await store.observeSession(true);
    expect(mocks.getStatus).not.toHaveBeenCalled();

    mocks.state.authenticated = true;
    mocks.state.binding = bindingFor(11);
    mocks.getStatus.mockResolvedValue(makeStatus(11));
    await store.observeSession(true);
    await store.observeSession(true);

    expect(mocks.getStatus).toHaveBeenCalledOnce();
    expect(store.status?.current_step).toBe('avatar');
    expect(store.isOpen).toBe(true);
  });

  it('ignores a delayed status response from the previous user', async () => {
    const store = useOnboardingStore();
    mocks.state.authenticated = true;
    mocks.state.binding = bindingFor(11);
    let resolveA!: (status: OnboardingStatus) => void;
    mocks.getStatus
      .mockImplementationOnce(() => new Promise(resolve => { resolveA = resolve; }))
      .mockResolvedValueOnce(makeStatus(22, { current_step: 'interests', interest_slugs: ['space'] }));

    const loadA = store.observeSession(true);
    mocks.state.binding = bindingFor(22);
    await store.observeSession(true);
    resolveA(makeStatus(11, { current_step: 'display_name' }));
    await loadA;

    expect(store.currentViewerID).toBe(22);
    expect(store.status?.current_step).toBe('interests');
    expect(store.status?.interest_slugs).toEqual(['space']);
  });

  it('uses the server interest response and persists progress without losing saved choices', async () => {
    const store = useOnboardingStore();
    mocks.state.authenticated = true;
    mocks.state.binding = bindingFor(11);
    mocks.getStatus.mockResolvedValue(makeStatus(11, { current_step: 'interests' }));
    await store.observeSession(true);
    const context = store.beginOperation();
    expect(context).not.toBeNull();
    mocks.saveInterests.mockResolvedValue(makeStatus(11, { current_step: 'interests', interest_slugs: ['business', 'ai'] }));
    mocks.saveProgress.mockResolvedValue(makeStatus(11, { current_step: 'follow_users', interest_slugs: ['ai', 'business'] }));

    const saved = await store.saveInterests(['ai', 'business'], context!);
    const progressed = await store.saveProgress('follow_users', context!);
    store.finishOperation(context!);

    expect(saved?.interest_slugs).toEqual(['business', 'ai']);
    expect(progressed).toBe(true);
    expect(store.status?.current_step).toBe('follow_users');
    expect(store.status?.interest_slugs).toEqual(['ai', 'business']);
    expect(mocks.saveInterests).toHaveBeenCalledWith(['ai', 'business']);
  });

  it('requires explicit server confirmation before closing and keeps temporary dismissal non-persistent', async () => {
    const store = useOnboardingStore();
    mocks.state.authenticated = true;
    mocks.state.binding = bindingFor(11);
    mocks.getStatus.mockResolvedValue(makeStatus(11));
    await store.observeSession(true);

    mocks.complete.mockResolvedValue(makeStatus(11, { completed: false }));
    await expect(store.complete()).resolves.toBe(false);
    expect(store.status?.completed).toBe(false);
    expect(store.isOpen).toBe(true);

    store.dismissTemporarily();
    expect(store.isOpen).toBe(false);
    expect(store.status?.completed).toBe(false);

    mocks.complete.mockResolvedValue(makeStatus(11, { completed: true, completed_at: '2026-10-15T00:00:00Z' }));
    await expect(store.complete()).resolves.toBe(true);
    expect(store.status?.completed).toBe(true);
    expect(store.isOpen).toBe(false);
  });

  it('does not let a delayed completion response for one user close another user’s setup', async () => {
    const store = useOnboardingStore();
    mocks.state.authenticated = true;
    mocks.state.binding = bindingFor(11);
    mocks.getStatus.mockResolvedValueOnce(makeStatus(11)).mockResolvedValueOnce(
      makeStatus(22, { current_step: 'interests', interest_slugs: ['ai'] }),
    );
    await store.observeSession(true);
    let resolveCompletion!: (status: OnboardingStatus) => void;
    mocks.complete.mockImplementationOnce(() => new Promise(resolve => { resolveCompletion = resolve; }));
    const completion = store.complete();

    mocks.state.binding = bindingFor(22);
    await store.observeSession(true);
    resolveCompletion(makeStatus(11, { completed: true, completed_at: '2026-10-15T00:00:00Z' }));
    await expect(completion).resolves.toBe(false);

    expect(store.currentViewerID).toBe(22);
    expect(store.status?.completed).toBe(false);
    expect(store.status?.interest_slugs).toEqual(['ai']);
    expect(store.isOpen).toBe(true);
  });
});
