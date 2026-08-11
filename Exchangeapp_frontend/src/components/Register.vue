<template>
  <main class="auth-page">
    <div class="auth-layout">
      <section class="auth-content" aria-labelledby="register-title">
        <div class="auth-brand">
          <span class="auth-brand__mobile-mark" aria-hidden="true">GX</span>
          <span class="auth-brand__name">Go Exchange</span>
        </div>

      <header class="auth-heading">

        <h1 id="register-title">Create your account</h1>
        <p>Join the financial conversation.</p>
      </header>

      <form class="auth-form" :aria-busy="submitting" @submit.prevent="register">
        <div class="auth-field">
          <label for="register-username">Username</label>
          <input
            id="register-username"
            v-model="form.username"
            name="username"
            type="text"
            autocomplete="username"
            autocapitalize="none"
            spellcheck="false"
            placeholder="Enter your username"
            :disabled="submitting"
          />
        </div>

        <div class="auth-field">
          <label for="register-password">Password</label>
          <input
            id="register-password"
            v-model="form.password"
            name="password"
            type="password"
            autocomplete="new-password"
            placeholder="Create a password"
            :disabled="submitting"
          />
        </div>

        <p v-if="formError" class="auth-error" role="alert" aria-live="assertive">
          {{ formError }}
        </p>

        <button
          class="auth-submit"
          type="submit"
          :disabled="submitting"
          :aria-busy="submitting"
        >
          {{ submitting ? 'Creating account…' : 'Sign up' }}
        </button>
      </form>

      <p class="auth-switch">
        <span>Already have an account?</span>
        <RouterLink :to="{ name: 'Login' }">Log in</RouterLink>
      </p>
      </section>

      <div class="auth-visual" aria-hidden="true">
        <span class="auth-visual__mark">GX</span>
      </div>
    </div>
  </main>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { useRouter } from 'vue-router';
import { useAuthStore } from '../store/auth';

const form = ref({
  username: '',
  password: '',
});
const submitting = ref(false);
const formError = ref('');

const authStore = useAuthStore();
const router = useRouter();

const formatRegisterError = (error: unknown) => {
  const message = error instanceof Error ? error.message : '';
  if (message === 'User already exists or database error') {
    return 'Could not create account. That username may already be in use.';
  }
  if (message === 'Invalid request data') {
    return 'Enter a username and password.';
  }
  return 'Could not create account. Please try again.';
};

const register = async () => {
  if (submitting.value) {
    return;
  }

  formError.value = '';
  if (form.value.username.length === 0) {
    formError.value = 'Enter your username.';
    return;
  }
  if (form.value.password.length === 0) {
    formError.value = 'Create a password.';
    return;
  }

  submitting.value = true;
  try {
    await authStore.register(form.value.username, form.value.password);
    void router.push({ name: 'Home' });
  } catch (error) {
    formError.value = formatRegisterError(error);
  } finally {
    submitting.value = false;
  }
};
</script>

<style scoped>
.auth-page {
  position: relative;
  display: grid;
  min-height: 100vh;
  min-height: 100svh;
  place-items: center;
  overflow-x: hidden;
  padding-inline: clamp(32px, 6vw, 96px);
  background:
    radial-gradient(ellipse at 15% 10%, rgba(29, 155, 240, 0.22), transparent 38%),
    radial-gradient(ellipse at 85% 14%, rgba(99, 102, 241, 0.14), transparent 34%),
    radial-gradient(ellipse at 50% 100%, rgba(14, 116, 144, 0.1), transparent 42%),
    linear-gradient(145deg, #020617 0%, #061326 48%, #020617 100%);
  color: #f8fafc;
}

.auth-page::before,
.auth-page::after {
  position: absolute;
  inset: 0;
  pointer-events: none;
  content: "";
}

.auth-page::before {
  background-image:
    radial-gradient(circle at 12% 18%, rgba(226, 242, 255, 0.82) 0 0.7px, transparent 1px),
    radial-gradient(circle at 62% 34%, rgba(186, 224, 255, 0.64) 0 0.8px, transparent 1.2px),
    radial-gradient(circle at 84% 76%, rgba(226, 242, 255, 0.68) 0 0.65px, transparent 1px);
  background-position: 24px 18px, 82px 44px, 12px 96px;
  background-size: 178px 214px, 246px 286px, 332px 390px;
  opacity: 0.62;
}

.auth-page::after {
  background:
    radial-gradient(circle at 28% 72%, rgba(29, 155, 240, 0.08), transparent 24%),
    radial-gradient(circle at 74% 28%, rgba(129, 140, 248, 0.06), transparent 22%);
  opacity: 0.9;
}

.auth-layout {
  position: relative;
  z-index: 1;

  display: grid;
  width: min(100%, 1440px);
  grid-template-columns: minmax(340px, 0.85fr) minmax(360px, 1.15fr);
  align-items: center;
  gap: clamp(40px, 6vw, 96px);
}

.auth-content {
  width: min(100%, 420px);
  min-width: 0;
  justify-self: start;
}

.auth-brand {
  display: grid;
  justify-items: start;
  gap: 10px;
  margin-bottom: 42px;
  text-align: left;
}

.auth-brand__mobile-mark {
  display: none;
  width: 48px;
  height: 48px;
  place-items: center;
  border: 1px solid rgba(125, 211, 252, 0.28);
  border-radius: 16px;
  background: rgba(29, 155, 240, 0.16);
  color: #e0f2fe;
  font-size: 15px;
  font-weight: 850;
  letter-spacing: 0.08em;
}

.auth-brand__name {
  color: #f8fafc;
  font-size: 15px;
  font-weight: 800;
  letter-spacing: 0.01em;
}

.auth-visual {
  display: grid;
  min-width: 0;
  min-height: clamp(300px, 42vw, 560px);
  place-items: center;
  pointer-events: none;
}

.auth-visual__mark {
  color: rgba(224, 242, 254, 0.88);
  font-size: clamp(200px, 26vw, 420px);
  font-weight: 900;
  letter-spacing: -0.1em;
  line-height: 0.78;
  user-select: none;
  white-space: nowrap;
  text-shadow: 0 10px 34px rgba(29, 155, 240, 0.1);
}

.auth-heading {
  margin-bottom: 32px;
}

.auth-heading h1 {
  margin: 0;
  color: #f8fafc;
  font-size: clamp(36px, 4vw, 46px);
  font-weight: 780;
  letter-spacing: -0.055em;
  line-height: 1.02;
}

.auth-heading > p:last-child {
  max-width: 34ch;
  margin: 14px 0 0;
  color: #94a3b8;
  font-size: 15px;
  line-height: 1.5;
}

.auth-form {
  display: grid;
  gap: 20px;
}

.auth-field {
  display: grid;
  gap: 9px;
}

.auth-field label {
  color: #e2e8f0;
  font-size: 13px;
  font-weight: 700;
}

.auth-field input {
  width: 100%;
  height: 50px;
  border: 1px solid rgba(148, 163, 184, 0.28);
  border-radius: 12px;
  padding: 0 15px;
  outline: 0;
  background: rgba(15, 31, 56, 0.72);
  color: #f8fafc;
  caret-color: #7dd3fc;
  font-size: 15px;
  transition:
    border-color var(--transition-fast),
    background-color var(--transition-fast),
    box-shadow var(--transition-fast);
}

.auth-field input::placeholder {
  color: #64748b;
}

.auth-field input:hover:not(:disabled) {
  border-color: rgba(148, 163, 184, 0.48);
}

.auth-field input:focus {
  border-color: var(--color-accent);
  background: rgba(15, 31, 56, 0.92);
  box-shadow: 0 0 0 3px rgba(29, 155, 240, 0.14);
}

.auth-field input:disabled {
  cursor: wait;
  opacity: 0.66;
}

.auth-field input:-webkit-autofill,
.auth-field input:-webkit-autofill:hover,
.auth-field input:-webkit-autofill:focus {
  -webkit-text-fill-color: #f8fafc;
  caret-color: #7dd3fc;
  box-shadow: 0 0 0 1000px #0d203a inset, 0 0 0 3px rgba(29, 155, 240, 0.12);
  transition: background-color 9999s ease-out 0s;
}

.auth-error {
  margin: -3px 0 0;
  color: #fda4af;
  font-size: 13px;
  line-height: 1.45;
}

.auth-submit {
  width: 100%;
  min-height: 50px;
  margin-top: 4px;
  border: 1px solid var(--color-accent);
  border-radius: var(--radius-pill);
  padding: 0 20px;
  background: var(--color-accent);
  color: #fff;
  cursor: pointer;
  font-size: 15px;
  font-weight: 750;
  transition:
    background-color var(--transition-fast),
    border-color var(--transition-fast),
    transform var(--transition-fast),
    box-shadow var(--transition-fast);
}

.auth-submit:hover:not(:disabled) {
  border-color: var(--color-accent-hover);
  background: var(--color-accent-hover);
  box-shadow: 0 8px 22px rgba(29, 155, 240, 0.22);
  transform: translateY(-1px);
}

.auth-submit:active:not(:disabled) {
  transform: translateY(0);
}

.auth-submit:disabled {
  cursor: wait;
  opacity: 0.7;
}

.auth-switch {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-start;
  gap: 5px;
  margin: 28px 0 0;
  color: #94a3b8;
  font-size: 13px;
  line-height: 1.5;
  text-align: left;
}

.auth-switch a {
  color: #7dd3fc;
  font-weight: 750;
  text-decoration: none;
}

.auth-switch a:hover,
.auth-switch a:focus-visible {
  color: #bae6fd;
  text-decoration: underline;
  text-underline-offset: 3px;
}

@media (max-width: 899px) {
  .auth-page {
    place-items: start center;
    padding-block: clamp(24px, 6vw, 48px);
  }

  .auth-layout {
    width: min(100%, 420px);
    grid-template-columns: minmax(0, 1fr);
    margin-block: auto;
  }

  .auth-content {
    width: 100%;
    justify-self: stretch;
  }

  .auth-visual {
    display: none;
  }

  .auth-brand {
    justify-items: center;
    margin-bottom: 32px;
    text-align: center;
  }

  .auth-brand__mobile-mark {
    display: grid;
  }

  .auth-heading h1 {
    font-size: clamp(36px, 8vw, 44px);
  }

  .auth-switch {
    justify-content: center;
    text-align: center;
  }
}

@media (max-width: 480px) {
  .auth-page {
    padding-inline: 16px;
    padding-top: max(24px, env(safe-area-inset-top));
    padding-bottom: max(24px, env(safe-area-inset-bottom));
  }

  .auth-brand {
    margin-bottom: 30px;
  }

  .auth-heading {
    margin-bottom: 28px;
  }

  .auth-heading h1 {
    font-size: 36px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .auth-field input,
  .auth-submit {
    transition: none;
  }

  .auth-submit:hover:not(:disabled),
  .auth-submit:active:not(:disabled) {
    transform: none;
  }
}
</style>
