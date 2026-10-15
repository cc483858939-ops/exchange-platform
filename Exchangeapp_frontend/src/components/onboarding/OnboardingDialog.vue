<template>
  <Teleport to="body">
    <dialog
      ref="dialogElement"
      class="onboarding-dialog"
      aria-labelledby="onboarding-title"
      :aria-busy="onboarding.loading || onboarding.saving"
      @cancel.prevent="handleCancel"
    >
      <div class="onboarding-dialog__panel">
        <header class="onboarding-dialog__header">
          <div class="onboarding-dialog__topline">
            <div class="onboarding-dialog__brand" aria-label="Exchange">
              <span aria-hidden="true">E</span>
              <strong>EXCHANGE</strong>
            </div>
            <div class="onboarding-dialog__top-actions">
              <button
                v-if="onboarding.temporaryDismissAvailable && !onboarding.saving"
                class="onboarding-dialog__continue"
                type="button"
                @click="onboarding.dismissTemporarily"
              >
                Continue to app for now
              </button>
              <button
                class="onboarding-dialog__skip-setup"
                type="button"
                :disabled="onboarding.loading || onboarding.saving"
                @click="skipSetup"
              >
                Skip setup
              </button>
            </div>
          </div>
          <div v-if="stepIndex > 0" class="onboarding-dialog__progress" role="progressbar" aria-label="Setup progress" :aria-valuemin="1" :aria-valuemax="4" :aria-valuenow="stepIndex">
            <span :style="{ width: `${stepIndex * 25}%` }"></span>
          </div>
          <div class="onboarding-dialog__heading">
            <p v-if="stepIndex > 0" class="onboarding-dialog__eyebrow">STEP {{ stepIndex }} OF 4</p>
            <h1 id="onboarding-title">{{ stepTitle }}</h1>
            <p class="onboarding-dialog__description">{{ stepDescription }}</p>
          </div>
        </header>

        <div v-if="onboarding.error" class="onboarding-dialog__error" role="alert">
          <span>{{ onboarding.error }}</span>
          <button v-if="statusUnavailable" type="button" :disabled="onboarding.loading || onboarding.saving" @click="onboarding.retry">
            Retry
          </button>
        </div>

        <div class="onboarding-dialog__content" :key="sessionKey">
          <div v-if="onboarding.loading && !onboarding.status" class="onboarding-dialog__loading" role="status">
            <span class="onboarding-dialog__spinner" aria-hidden="true"></span>
            <span>Restoring your setup…</span>
          </div>
          <div v-else-if="!onboarding.status" class="onboarding-dialog__loading">
            <span class="onboarding-dialog__status-icon" aria-hidden="true">↗</span>
            <span>Your saved steps will stay here until you're ready to return.</span>
          </div>
          <component
            v-else
            :is="activeStepComponent"
            ref="stepController"
            :key="`${sessionKey}:${onboarding.status.current_step}`"
          />
        </div>

        <footer v-if="onboarding.status" class="onboarding-dialog__footer">
          <button
            class="onboarding-dialog__skip-step"
            type="button"
            :disabled="onboarding.saving || onboarding.loading"
            @click="skipStep"
          >
            Skip this step
          </button>
          <button
            class="onboarding-dialog__next"
            type="button"
            :disabled="onboarding.saving || onboarding.loading || !canAdvance"
            :aria-busy="onboarding.saving"
            @click="nextStep"
          >
            <span v-if="onboarding.saving" class="onboarding-dialog__spinner" aria-hidden="true"></span>
            {{ onboarding.saving ? 'Saving…' : isFinalStep ? 'Finish' : 'Next' }}
          </button>
        </footer>
      </div>
    </dialog>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import type { ComponentPublicInstance } from 'vue';
import OnboardingAvatarStep from './OnboardingAvatarStep.vue';
import OnboardingDisplayNameStep from './OnboardingDisplayNameStep.vue';
import OnboardingInterestsStep from './OnboardingInterestsStep.vue';
import OnboardingFollowStep from './OnboardingFollowStep.vue';
import { useAuthStore } from '../../store/auth';
import { useOnboardingStore } from '../../store/onboarding';

const props = defineProps<{ enabled: boolean }>();

type StepControls = ComponentPublicInstance & {
  next: () => Promise<void>;
  skip: () => Promise<void>;
  canAdvance: boolean;
};

const authStore = useAuthStore();
const onboarding = useOnboardingStore();
const dialogElement = ref<HTMLDialogElement | null>(null);
const stepController = ref<StepControls | null>(null);

const stepIndex = computed(() => {
  switch (onboarding.status?.current_step) {
    case 'avatar': return 1;
    case 'display_name': return 2;
    case 'interests': return 3;
    case 'follow_users': return 4;
    default: return 0;
  }
});
const stepTitle = computed(() => {
  switch (onboarding.status?.current_step) {
    case 'avatar': return 'Choose a profile picture';
    case 'display_name': return 'What should we call you?';
    case 'interests': return 'What are you interested in?';
    case 'follow_users': return "Don't miss out";
    default: return onboarding.error ? 'Your setup is waiting' : 'Let’s make this feel like yours';
  }
});
const stepDescription = computed(() => {
  switch (onboarding.status?.current_step) {
    case 'avatar': return 'Show people who you are. You can always change this later.';
    case 'display_name': return 'Choose a display name. You can change it at any time.';
    case 'interests': return "Choose topics you like. We'll use these preferences to improve your experience.";
    case 'follow_users': return 'Follow people to see more from them.';
    default: return onboarding.error ? 'We could not restore your saved setup just now.' : 'A few quick steps will help you settle in.';
  }
});
const activeStepComponent = computed(() => {
  switch (onboarding.status?.current_step) {
    case 'avatar': return OnboardingAvatarStep;
    case 'display_name': return OnboardingDisplayNameStep;
    case 'interests': return OnboardingInterestsStep;
    case 'follow_users': return OnboardingFollowStep;
    default: return null;
  }
});
const isFinalStep = computed(() => onboarding.status?.current_step === 'follow_users');
const canAdvance = computed(() => stepController.value?.canAdvance !== false);
const statusUnavailable = computed(() => onboarding.status === null && Boolean(onboarding.error));
const sessionKey = computed(() => `${onboarding.currentViewerID ?? 'none'}:${authStore.sessionVersion}`);
const isVisible = computed(() => props.enabled && onboarding.isOpen);

watch(
  [() => props.enabled, () => authStore.isAuthenticated, () => authStore.currentIdentity?.id, () => authStore.sessionVersion],
  ([enabled]) => { void onboarding.observeSession(Boolean(enabled)); },
  { immediate: true, flush: 'sync' },
);

watch(isVisible, async (visible) => {
  await nextTick();
  const dialog = dialogElement.value;
  if (!dialog) return;
  if (visible && !dialog.open) {
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
    await nextTick();
    dialog.querySelector<HTMLElement>('.onboarding-dialog__skip-setup')?.focus();
  } else if (!visible && dialog.open) {
    if (typeof dialog.close === 'function') dialog.close();
    else dialog.removeAttribute('open');
  }
}, { immediate: true });

const nextStep = () => { void stepController.value?.next?.(); };
const skipStep = () => { void stepController.value?.skip?.(); };
const skipSetup = () => { void onboarding.complete(); };

const handleCancel = (event: Event) => {
  event.preventDefault();
  if (onboarding.temporaryDismissAvailable && !onboarding.saving) onboarding.dismissTemporarily();
};

onBeforeUnmount(() => {
  const dialog = dialogElement.value;
  if (dialog?.open) {
    if (typeof dialog.close === 'function') dialog.close();
    else dialog.removeAttribute('open');
  }
});
</script>

<style scoped>
.onboarding-dialog { width: min(600px, calc(100vw - 32px)); max-width: calc(100vw - 32px); max-height: min(760px, calc(100dvh - 48px)); box-sizing: border-box; margin: auto; border: 1px solid var(--color-border); border-radius: 24px; padding: 0; overflow: hidden; background: var(--color-surface); color: var(--color-text); box-shadow: 0 28px 88px rgba(15, 20, 25, .28); }
.onboarding-dialog::backdrop { background: rgba(15, 20, 25, .62); backdrop-filter: blur(2px); }
.onboarding-dialog__panel { display: grid; max-height: inherit; grid-template-rows: auto auto minmax(0, 1fr) auto; }
.onboarding-dialog__header { padding: var(--space-5) var(--space-6) 0; }
.onboarding-dialog__topline { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
.onboarding-dialog__brand { display: flex; align-items: center; gap: var(--space-2); color: var(--color-text); letter-spacing: .12em; font-size: 11px; }
.onboarding-dialog__brand span { display: grid; width: 27px; height: 27px; place-items: center; border-radius: 9px; background: var(--color-text); color: var(--color-surface); font-size: 15px; font-weight: 850; letter-spacing: 0; }
.onboarding-dialog__top-actions { display: flex; align-items: center; gap: var(--space-3); }
.onboarding-dialog__skip-setup, .onboarding-dialog__continue { min-height: 40px; border: 0; background: transparent; color: var(--color-text-secondary); font: inherit; font-size: 12px; font-weight: 700; cursor: pointer; }
.onboarding-dialog__skip-setup:hover:not(:disabled), .onboarding-dialog__continue:hover { color: var(--color-accent); text-decoration: underline; text-underline-offset: 3px; }
.onboarding-dialog__skip-setup:disabled { cursor: wait; opacity: .55; }
.onboarding-dialog__progress { height: 3px; margin: var(--space-5) calc(-1 * var(--space-6)) 0; background: var(--color-surface-subtle); }
.onboarding-dialog__progress span { display: block; height: 100%; background: var(--color-accent); transition: width 180ms ease; }
.onboarding-dialog__heading { padding: var(--space-5) 0 var(--space-3); text-align: left; }
.onboarding-dialog__eyebrow { margin: 0 0 var(--space-2); color: var(--color-accent); font-size: 11px; font-weight: 800; letter-spacing: .14em; }
.onboarding-dialog__heading h1 { margin: 0; color: var(--color-text); font-size: clamp(24px, 4vw, 30px); font-weight: 800; letter-spacing: -.035em; line-height: 1.12; }
.onboarding-dialog__description { max-width: 420px; margin: var(--space-2) 0 0; color: var(--color-text-secondary); font-size: 14px; line-height: 1.5; }
.onboarding-dialog__error { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); margin: 0 var(--space-6) var(--space-2); border: 1px solid color-mix(in srgb, var(--color-danger) 24%, transparent); border-radius: var(--radius-sm); padding: var(--space-3); background: color-mix(in srgb, var(--color-danger) 6%, var(--color-surface)); color: var(--color-danger); font-size: 13px; line-height: 1.4; }
.onboarding-dialog__error button { flex: 0 0 auto; min-height: 36px; border: 1px solid currentColor; border-radius: var(--radius-pill); padding-inline: var(--space-4); background: transparent; color: inherit; font: inherit; font-weight: 700; cursor: pointer; }
.onboarding-dialog__content { min-height: 0; overflow: auto; overscroll-behavior: contain; scrollbar-gutter: stable; }
.onboarding-dialog__loading { display: grid; min-height: 220px; align-content: center; justify-items: center; gap: var(--space-3); padding: var(--space-6); color: var(--color-text-secondary); font-size: 14px; text-align: center; }
.onboarding-dialog__spinner { display: inline-block; width: 15px; height: 15px; border: 2px solid color-mix(in srgb, var(--color-accent) 25%, transparent); border-top-color: var(--color-accent); border-radius: 50%; animation: onboarding-spin .7s linear infinite; }
.onboarding-dialog__status-icon { display: grid; width: 46px; height: 46px; place-items: center; border-radius: 50%; background: var(--color-surface-subtle); color: var(--color-accent); font-size: 24px; }
.onboarding-dialog__footer { position: sticky; bottom: 0; display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); border-top: 1px solid var(--color-border); padding: var(--space-4) var(--space-6); background: var(--color-surface); }
.onboarding-dialog__skip-step { min-height: 46px; border: 0; border-radius: var(--radius-pill); padding: 0 var(--space-4); background: transparent; color: var(--color-text-secondary); font: inherit; font-size: 13px; font-weight: 700; cursor: pointer; }
.onboarding-dialog__skip-step:hover:not(:disabled) { background: var(--color-surface-subtle); color: var(--color-text); }
.onboarding-dialog__next { display: inline-flex; min-width: 120px; min-height: 48px; align-items: center; justify-content: center; gap: var(--space-2); border: 1px solid var(--color-text); border-radius: var(--radius-pill); padding: 0 var(--space-6); background: var(--color-text); color: var(--color-surface); font: inherit; font-size: 14px; font-weight: 800; cursor: pointer; }
.onboarding-dialog__next:hover:not(:disabled) { background: var(--color-accent); border-color: var(--color-accent); }
.onboarding-dialog__next:disabled, .onboarding-dialog__skip-step:disabled { cursor: wait; opacity: .56; }
.onboarding-dialog button:focus-visible { outline: 3px solid color-mix(in srgb, var(--color-accent) 45%, transparent); outline-offset: 3px; }
@keyframes onboarding-spin { to { transform: rotate(360deg); } }
@media (max-width: 640px) {
  .onboarding-dialog { width: 100vw; max-width: 100vw; height: 100dvh; max-height: 100dvh; margin: 0; border: 0; border-radius: 0; }
  .onboarding-dialog__panel { box-sizing: border-box; height: 100%; max-height: 100%; grid-template-rows: auto auto minmax(0, 1fr) auto; padding-top: var(--mobile-safe-top); padding-bottom: var(--mobile-safe-bottom); }
  .onboarding-dialog__header { padding: var(--space-4) var(--space-4) 0; }
  .onboarding-dialog__progress { margin-inline: calc(-1 * var(--space-4)); }
  .onboarding-dialog__heading { padding-top: var(--space-4); }
  .onboarding-dialog__error { margin-inline: var(--space-4); }
  .onboarding-dialog__footer { padding-inline: var(--space-4); }
  .onboarding-dialog__next { min-width: 112px; }
}
@media (max-width: 380px) {
  .onboarding-dialog__header { padding-inline: var(--space-3); }
  .onboarding-dialog__top-actions { gap: var(--space-1); }
  .onboarding-dialog__skip-setup, .onboarding-dialog__continue { padding-inline: var(--space-1); font-size: 11px; }
  .onboarding-dialog__footer { padding-inline: var(--space-3); }
}
@media (prefers-reduced-motion: reduce) { .onboarding-dialog__spinner { animation-duration: 1.8s; } .onboarding-dialog__progress span { transition: none; } }
</style>
