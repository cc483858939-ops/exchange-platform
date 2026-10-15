import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import type { AuthRequestBinding } from '../auth/authRequestBinding';
import { useAuthStore } from './auth';
import {
  completeOnboarding as completeOnboardingRequest,
  getOnboardingStatus,
  saveOnboardingInterests,
  saveOnboardingProgress,
  type OnboardingStatus,
  type OnboardingStep,
} from '../services/onboardingService';

export type OnboardingRequestContext = Readonly<{
  binding: AuthRequestBinding;
  generation: number;
}>;

const validSteps = new Set<OnboardingStep>([
  'avatar', 'display_name', 'interests', 'follow_users',
]);

const parseStatus = (value: OnboardingStatus): OnboardingStatus => {
  if (
    !value
    || typeof value.completed !== 'boolean'
    || !validSteps.has(value.current_step)
    || !Array.isArray(value.interest_slugs)
    || !value.interest_slugs.every(slug => typeof slug === 'string')
    || !(value.completed_at === null || typeof value.completed_at === 'string')
  ) throw new Error('Invalid onboarding response');
  return {
    completed: value.completed,
    current_step: value.current_step,
    interest_slugs: [...value.interest_slugs],
    completed_at: value.completed_at,
  };
};

const sameBinding = (
  left: AuthRequestBinding | null,
  right: AuthRequestBinding | null,
) => Boolean(
  left && right
  && left.userID === right.userID
  && left.sessionID === right.sessionID
  && left.sessionVersion === right.sessionVersion,
);

export const useOnboardingStore = defineStore('onboarding', () => {
  const authStore = useAuthStore();
  const status = ref<OnboardingStatus | null>(null);
  const loading = ref(false);
  const saving = ref(false);
  const error = ref('');
  const temporaryDismissAvailable = ref(false);
  const currentViewerID = ref<number | null>(null);
  const temporarilyDismissed = ref(false);

  let activeBinding: AuthRequestBinding | null = null;
  let requestGeneration = 0;
  let loadAttempted = false;

  const isOpen = computed(() => (
    currentViewerID.value !== null
    && !temporarilyDismissed.value
    && (loading.value || Boolean(error.value) || Boolean(status.value && !status.value.completed))
  ));

  const resetForBinding = (binding: AuthRequestBinding | null) => {
    requestGeneration += 1;
    activeBinding = binding;
    currentViewerID.value = binding?.userID ?? null;
    status.value = null;
    loading.value = false;
    saving.value = false;
    error.value = '';
    temporaryDismissAvailable.value = false;
    temporarilyDismissed.value = false;
    loadAttempted = false;
  };

  const observeSession = async (shouldLoad: boolean) => {
    const binding = authStore.captureRequestAuthBinding();
    if (!binding) {
      if (activeBinding || currentViewerID.value !== null) resetForBinding(null);
      return;
    }
    if (!sameBinding(activeBinding, binding)) resetForBinding(binding);
    if (!shouldLoad || loadAttempted) return;

    const context = Object.freeze({ binding, generation: requestGeneration });
    loadAttempted = true;
    loading.value = true;
    error.value = '';
    temporaryDismissAvailable.value = false;
    try {
      const response = parseStatus(await getOnboardingStatus());
      if (!matchesContext(context)) return;
      status.value = response;
    } catch {
      if (matchesContext(context)) {
        error.value = 'Could not load your setup progress. Retry or continue to the app for now.';
        temporaryDismissAvailable.value = true;
      }
    } finally {
      if (matchesContext(context)) loading.value = false;
    }
  };

  const captureContext = (): OnboardingRequestContext | null => {
    if (!activeBinding || !authStore.matchesRequestAuthBinding(activeBinding)) return null;
    return Object.freeze({ binding: activeBinding, generation: requestGeneration });
  };

  const matchesContext = (context: OnboardingRequestContext) => (
    context.generation === requestGeneration
    && sameBinding(activeBinding, context.binding)
    && authStore.matchesRequestAuthBinding(context.binding)
  );

  const beginOperation = (): OnboardingRequestContext | null => {
    if (saving.value || loading.value) return null;
    const context = captureContext();
    if (!context) return null;
    saving.value = true;
    error.value = '';
    temporaryDismissAvailable.value = false;
    return context;
  };

  const finishOperation = (context: OnboardingRequestContext) => {
    if (matchesContext(context)) saving.value = false;
  };

  const saveProgress = async (
    step: OnboardingStep,
    context: OnboardingRequestContext,
  ): Promise<boolean> => {
    try {
      const nextStatus = parseStatus(await saveOnboardingProgress(step));
      if (!matchesContext(context)) return false;
      status.value = nextStatus;
      return true;
    } catch {
      if (matchesContext(context)) {
        error.value = 'Could not save your progress. Please retry.';
        temporaryDismissAvailable.value = true;
      }
      return false;
    }
  };

  const saveInterests = async (
    slugs: string[],
    context: OnboardingRequestContext,
  ): Promise<OnboardingStatus | null> => {
    try {
      const nextStatus = parseStatus(await saveOnboardingInterests(slugs));
      if (!matchesContext(context)) return null;
      status.value = nextStatus;
      return nextStatus;
    } catch {
      if (matchesContext(context)) {
        error.value = 'Could not save your interests. Please retry.';
        temporaryDismissAvailable.value = true;
      }
      return null;
    }
  };

  const complete = async (): Promise<boolean> => {
    const context = beginOperation();
    if (!context) return false;
    try {
      const completedStatus = parseStatus(await completeOnboardingRequest());
      if (!matchesContext(context)) return false;
      if (completedStatus.completed !== true) {
        error.value = 'The server has not confirmed setup completion. Please retry.';
        temporaryDismissAvailable.value = true;
        return false;
      }
      status.value = completedStatus;
      return true;
    } catch {
      if (matchesContext(context)) {
        error.value = 'Could not complete setup. Please retry or continue to the app for now.';
        temporaryDismissAvailable.value = true;
      }
      return false;
    } finally {
      finishOperation(context);
    }
  };

  const retry = async () => {
    if (loading.value || saving.value) return;
    loadAttempted = false;
    await observeSession(true);
  };

  const dismissTemporarily = () => {
    if (currentViewerID.value !== null && temporaryDismissAvailable.value && !saving.value) {
      temporarilyDismissed.value = true;
    }
  };

  const setError = (message: string, allowTemporaryDismiss = false) => {
    error.value = message;
    temporaryDismissAvailable.value = allowTemporaryDismiss;
  };

  const clearError = () => {
    error.value = '';
    temporaryDismissAvailable.value = false;
  };

  return {
    status,
    loading,
    saving,
    error,
    temporaryDismissAvailable,
    currentViewerID,
    temporarilyDismissed,
    isOpen,
    observeSession,
    captureContext,
    matchesContext,
    beginOperation,
    finishOperation,
    saveProgress,
    saveInterests,
    complete,
    retry,
    dismissTemporarily,
    setError,
    clearError,
  };
});
