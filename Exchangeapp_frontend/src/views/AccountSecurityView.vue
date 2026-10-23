<template>
  <main class="account-security-view">
    <header class="account-security-view__header">
      <button class="account-security-view__back" type="button" aria-label="Back" @click="goBack">
        <AppIcon name="arrow-left" :size="20" />
      </button>
      <div class="account-security-view__heading">
        <p class="account-security-view__eyebrow">YOUR ACCOUNT</p>
        <h1>Security</h1>
      </div>
      <MobileAccountMenu v-if="authStore.isAuthenticated" />
    </header>

    <section v-if="!authStore.isAuthenticated" class="account-security-view__notice">
      <p v-if="errorMessage" role="alert">{{ errorMessage }}</p>
      <h2>Sign in to change your password.</h2>
      <RouterLink class="account-security-view__action" :to="{ name: 'Login', query: { returnTo: '/settings/security' } }">
        Sign in
      </RouterLink>
    </section>
    <form v-else class="account-security-view__form" @submit.prevent="submitChange">
      <p class="account-security-view__intro">Choose a new password for your account.</p>

      <label class="account-security-view__field">
        <span>Current password</span>
        <input
          v-model="currentPassword"
          autocomplete="current-password"
          type="password"
          required
          :disabled="isSubmitting"
        >
      </label>
      <label class="account-security-view__field">
        <span>New password</span>
        <input
          v-model="newPassword"
          autocomplete="new-password"
          type="password"
          required
          :disabled="isSubmitting"
          aria-describedby="password-byte-count"
        >
      </label>
      <p id="password-byte-count" class="account-security-view__hint" :class="{ 'account-security-view__hint--invalid': passwordBytes > 72 }">
        {{ passwordBytes }} / 72 UTF-8 bytes
      </p>
      <label class="account-security-view__field">
        <span>Confirm new password</span>
        <input
          v-model="confirmation"
          autocomplete="new-password"
          type="password"
          required
          :disabled="isSubmitting"
        >
      </label>

      <p v-if="errorMessage" class="account-security-view__error" role="alert">{{ errorMessage }}</p>
      <button
        class="account-security-view__submit"
        type="submit"
        :disabled="!canSubmit || isSubmitting"
        :aria-busy="isSubmitting"
      >
        {{ isSubmitting ? 'Changing password…' : 'Change password' }}
      </button>
    </form>
  </main>
</template>

<script setup lang="ts">
import { computed, ref, shallowRef, watch } from 'vue';
import { useRouter } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import MobileAccountMenu from '../components/layout/MobileAccountMenu.vue';
import { useAuthStore } from '../store/auth';
import { AuthSessionChangedError } from '../store/auth';
import type { AuthRequestBinding } from '../auth/authRequestBinding';

const authStore = useAuthStore();
const router = useRouter();
const currentPassword = ref('');
const newPassword = ref('');
const confirmation = ref('');
const errorMessage = ref('');
const isSubmitting = ref(false);
const formBinding = shallowRef<AuthRequestBinding | null>(authStore.captureRequestAuthBinding());
const formGeneration = ref(0);
const passwordBytes = computed(() => new TextEncoder().encode(newPassword.value).byteLength);
const canSubmit = computed(() => Boolean(
  authStore.isAuthenticated
  && formBinding.value
  && authStore.matchesRequestAuthBinding(formBinding.value)
  && !isSubmitting.value
  && currentPassword.value
  && newPassword.value
  && confirmation.value
  && passwordBytes.value <= 72
  && newPassword.value === confirmation.value
  && newPassword.value !== currentPassword.value,
));

const clearPasswordForm = () => {
  currentPassword.value = '';
  newPassword.value = '';
  confirmation.value = '';
  errorMessage.value = '';
};

watch(
  [
    () => authStore.isAuthenticated,
    () => authStore.currentIdentity?.id ?? null,
    () => authStore.sessionVersion,
  ],
  () => {
    formGeneration.value += 1;
    clearPasswordForm();
    isSubmitting.value = false;
    formBinding.value = authStore.isAuthenticated
      ? authStore.captureRequestAuthBinding()
      : null;
  },
  { flush: 'sync' },
);

const goBack = () => {
  const historyState = window.history.state as { back?: string | null } | null;
  if (historyState?.back) void router.back();
  else void router.push({ name: 'Home' });
};

const submitChange = async () => {
  if (isSubmitting.value) return;
  errorMessage.value = '';

  const requestBinding = formBinding.value;
  const requestGeneration = formGeneration.value;
  if (
    !authStore.isAuthenticated
    || !requestBinding
    || !authStore.matchesRequestAuthBinding(requestBinding)
  ) {
    formGeneration.value += 1;
    clearPasswordForm();
    isSubmitting.value = false;
    formBinding.value = authStore.isAuthenticated
      ? authStore.captureRequestAuthBinding()
      : null;
    errorMessage.value = 'Your account session changed. Review the form and try again.';
    return;
  }

  const currentPasswordValue = currentPassword.value;
  const newPasswordValue = newPassword.value;
  const confirmationValue = confirmation.value;
  if (passwordBytes.value > 72) {
    errorMessage.value = 'New password must be 72 UTF-8 bytes or fewer.';
    return;
  }
  if (newPasswordValue !== confirmationValue) {
    errorMessage.value = 'New password confirmation does not match.';
    return;
  }
  if (newPasswordValue === currentPasswordValue) {
    errorMessage.value = 'Choose a password different from your current password.';
    return;
  }

  isSubmitting.value = true;
  try {
    await authStore.changePassword(currentPasswordValue, newPasswordValue, requestBinding);
    if (formGeneration.value === requestGeneration) clearPasswordForm();
    if (!authStore.isAuthenticated) {
      await router.replace({ name: 'Login', query: { passwordChanged: '1' } });
    }
  } catch (error) {
    if (formGeneration.value === requestGeneration) {
      if (error instanceof AuthSessionChangedError) {
        errorMessage.value = 'Your account session changed while the request was in progress.';
      } else {
        errorMessage.value = error instanceof Error ? error.message : 'Password could not be changed. Please try again.';
      }
    }
  } finally {
    if (formGeneration.value === requestGeneration) isSubmitting.value = false;
  }
};
</script>

<style scoped>
.account-security-view {
  display: flex;
  width: min(100%, 760px);
  min-height: 100%;
  flex-direction: column;
  margin: 0 auto;
  padding: 24px clamp(16px, 4vw, 32px) 64px;
  box-sizing: border-box;
  color: var(--color-text);
}

.account-security-view__header {
  display: flex;
  min-height: 72px;
  align-items: center;
  gap: 14px;
  border-bottom: 1px solid var(--color-border);
  padding-bottom: 18px;
}

.account-security-view__back {
  display: grid;
  width: 44px;
  height: 44px;
  flex: 0 0 auto;
  place-items: center;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
}

.account-security-view__heading { flex: 1 1 auto; min-width: 0; }
.account-security-view__eyebrow { margin: 0 0 4px; color: var(--color-accent); font-size: 11px; font-weight: 800; letter-spacing: .14em; }
.account-security-view h1 { margin: 0; font-size: clamp(24px, 4vw, 32px); letter-spacing: -.035em; }
.account-security-view__form { display: grid; width: min(100%, 440px); gap: 16px; padding-top: 24px; }
.account-security-view__intro { margin: 0; color: var(--color-text-secondary); }
.account-security-view__field { display: grid; gap: 7px; font-size: 14px; font-weight: 650; }
.account-security-view__field input { min-height: 44px; border: 1px solid var(--color-border-strong, var(--color-border)); border-radius: var(--radius-sm); padding: 0 12px; background: var(--color-surface); color: var(--color-text); font: inherit; font-weight: 400; }
.account-security-view__field input:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
.account-security-view__hint { margin: -10px 0 0; color: var(--color-text-secondary); font-size: 12px; }
.account-security-view__hint--invalid,.account-security-view__error { color: var(--color-danger); }
.account-security-view__error { margin: 0; font-size: 14px; }
.account-security-view__submit,.account-security-view__action { display: inline-flex; width: fit-content; min-height: 42px; align-items: center; justify-content: center; border: 0; border-radius: var(--radius-pill); padding: 0 18px; background: var(--color-accent); color: white; cursor: pointer; font: inherit; font-weight: 700; text-decoration: none; }
.account-security-view__submit:disabled { cursor: not-allowed; opacity: .55; }
.account-security-view__notice { display: grid; min-height: 260px; align-content: center; justify-items: start; gap: 12px; color: var(--color-text-secondary); }
.account-security-view__notice h2,.account-security-view__notice p { margin: 0; }
.account-security-view__notice h2 { color: var(--color-text); font-size: 18px; }
</style>
