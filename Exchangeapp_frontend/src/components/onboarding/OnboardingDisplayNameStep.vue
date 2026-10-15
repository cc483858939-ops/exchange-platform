<template>
  <section class="onboarding-name" aria-labelledby="onboarding-name-label">
    <label id="onboarding-name-label" class="onboarding-name__label" for="onboarding-display-name">Display name</label>
    <input
      id="onboarding-display-name"
      v-model="draft"
      class="onboarding-name__input"
      type="text"
      autocomplete="name"
      :aria-describedby="nameHelpID"
      :aria-invalid="overLimit"
      :disabled="onboarding.saving"
      @input="onboarding.clearError"
    />
    <div class="onboarding-name__meta">
      <span :id="nameHelpID" class="onboarding-name__username">Your username: <strong>@{{ identity?.username }}</strong></span>
      <span :class="{ 'onboarding-name__count--over': overLimit }" aria-live="polite">{{ nameLength }}/50</span>
    </div>
    <p v-if="overLimit" class="onboarding-name__hint" role="alert">Display names can be up to 50 characters.</p>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { updateUserProfile } from '../../services/userService';
import { useAuthStore } from '../../store/auth';
import { useOnboardingStore } from '../../store/onboarding';
import { useProfileSessionStore } from '../../store/profileSession';
import type { OnboardingRequestContext } from '../../store/onboarding';

const authStore = useAuthStore();
const profileStore = useProfileSessionStore();
const onboarding = useOnboardingStore();
const identity = computed(() => authStore.currentIdentity);
const draft = ref(identity.value?.display_name ?? '');
const nameLength = computed(() => Array.from(draft.value.trim()).length);
const overLimit = computed(() => nameLength.value > 50);
const nameHelpID = 'onboarding-display-name-help';

const saveProgress = async (context: OnboardingRequestContext) => {
  await onboarding.saveProgress('interests', context);
};

const next = async () => {
  if (overLimit.value) {
    onboarding.setError('Display names can be up to 50 characters.');
    return;
  }
  const context = onboarding.beginOperation();
  if (!context) return;
  try {
    const displayName = draft.value.trim();
    const currentName = identity.value?.display_name.trim() ?? '';
    if (displayName && displayName !== currentName) {
      const updated = await updateUserProfile(context.binding.userID, { display_name: displayName });
      if (!onboarding.matchesContext(context)) return;
      if (updated.id !== context.binding.userID) throw new Error('Profile response did not match the signed-in user.');
      if (!authStore.syncCurrentIdentityProfile(updated)) throw new Error('Could not sync the saved profile.');
      profileStore.updateUser(updated);
    }
    await saveProgress(context);
  } catch {
    if (onboarding.matchesContext(context)) onboarding.setError('Could not save your display name. Please retry.', true);
  } finally {
    onboarding.finishOperation(context);
  }
};

const skip = async () => {
  draft.value = identity.value?.display_name ?? '';
  const context = onboarding.beginOperation();
  if (!context) return;
  try {
    await saveProgress(context);
  } finally {
    onboarding.finishOperation(context);
  }
};

defineExpose({ next, skip, canAdvance: computed(() => !overLimit.value) });
</script>

<style scoped>
.onboarding-name { display: grid; align-content: start; gap: var(--space-3); padding: var(--space-5) var(--space-4); }
.onboarding-name__label { color: var(--color-text); font-size: 14px; font-weight: 750; }
.onboarding-name__input { width: 100%; min-height: 54px; box-sizing: border-box; border: 1px solid var(--color-border-strong); border-radius: var(--radius-md); padding: 0 var(--space-4); background: var(--color-surface); color: var(--color-text); font: inherit; font-size: 17px; }
.onboarding-name__input:focus { border-color: var(--color-accent); outline: 2px solid color-mix(in srgb, var(--color-accent) 30%, transparent); outline-offset: 1px; }
.onboarding-name__meta { display: flex; justify-content: space-between; gap: var(--space-3); color: var(--color-text-tertiary); font-size: 13px; }
.onboarding-name__username strong { color: var(--color-text-secondary); font-weight: 650; }
.onboarding-name__count--over, .onboarding-name__hint { color: var(--color-danger); }
.onboarding-name__hint { margin: 0; font-size: 13px; }
</style>
