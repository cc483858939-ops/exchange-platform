import apiClient from '../axios';

export type OnboardingStep = 'avatar' | 'display_name' | 'interests' | 'follow_users';

export type OnboardingStatus = {
  completed: boolean;
  current_step: OnboardingStep;
  interest_slugs: string[];
  completed_at: string | null;
};

export async function getOnboardingStatus(): Promise<OnboardingStatus> {
  const response = await apiClient.get<OnboardingStatus>('/me/onboarding');
  return response.data;
}

export async function saveOnboardingProgress(currentStep: OnboardingStep): Promise<OnboardingStatus> {
  const response = await apiClient.put<OnboardingStatus>('/me/onboarding/progress', {
    current_step: currentStep,
  });
  return response.data;
}

export async function saveOnboardingInterests(topicSlugs: string[]): Promise<OnboardingStatus> {
  const response = await apiClient.put<OnboardingStatus>('/me/onboarding/interests', {
    topic_slugs: topicSlugs,
  });
  return response.data;
}

export async function completeOnboarding(): Promise<OnboardingStatus> {
  const response = await apiClient.put<OnboardingStatus>('/me/onboarding/complete');
  return response.data;
}
