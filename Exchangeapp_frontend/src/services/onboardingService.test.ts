import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));

vi.mock('../axios', () => ({ default: { get: mocks.get, put: mocks.put } }));

import {
  completeOnboarding,
  getOnboardingStatus,
  saveOnboardingInterests,
  saveOnboardingProgress,
} from './onboardingService';

describe('onboardingService', () => {
  beforeEach(() => {
    mocks.get.mockReset();
    mocks.put.mockReset();
  });

  it('uses the authenticated onboarding endpoints and payloads', async () => {
    const status = {
      completed: false,
      current_step: 'interests',
      interest_slugs: ['ai'],
      completed_at: null,
    };
    mocks.get.mockResolvedValue({ data: status });
    mocks.put.mockResolvedValue({ data: status });

    await expect(getOnboardingStatus()).resolves.toEqual(status);
    await saveOnboardingProgress('display_name');
    await saveOnboardingInterests(['ai', 'business']);
    await completeOnboarding();

    expect(mocks.get).toHaveBeenCalledWith('/me/onboarding');
    expect(mocks.put).toHaveBeenNthCalledWith(1, '/me/onboarding/progress', { current_step: 'display_name' });
    expect(mocks.put).toHaveBeenNthCalledWith(2, '/me/onboarding/interests', { topic_slugs: ['ai', 'business'] });
    expect(mocks.put).toHaveBeenNthCalledWith(3, '/me/onboarding/complete');
  });
});
