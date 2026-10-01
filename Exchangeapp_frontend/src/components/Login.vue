<template>
  <main class="auth-page">
    <div class="auth-layout">
      <section class="auth-content" aria-labelledby="login-title">
        <div class="auth-brand">
          <span class="auth-brand__mobile-mark" aria-hidden="true"><BrandMark /></span>
          <span class="auth-brand__name">Exchange</span>
        </div>

      <header class="auth-heading">

        <h1 id="login-title">Welcome back</h1>
        <p>Sign in to continue to your financial feed.</p>
      </header>

      <form class="auth-form" :aria-busy="submitting" @submit.prevent="login">
        <div class="auth-field">
          <label for="login-username">Username</label>
          <input
            id="login-username"
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
          <label for="login-password">Password</label>
          <input
            id="login-password"
            v-model="form.password"
            name="password"
            type="password"
            autocomplete="current-password"
            placeholder="Enter your password"
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
          {{ submitting ? 'Signing in…' : 'Log in' }}
        </button>
      </form>

      <p class="auth-switch">
        <span>Don't have an account?</span>
        <RouterLink :to="{ name: 'Register', query: authFlowQuery }">Sign up</RouterLink>
      </p>
      </section>

      <div class="auth-visual" aria-hidden="true">
        <span class="auth-visual__mark"><BrandMark /></span>
      </div>
    </div>
  </main>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { resolveAuthSuccessDestination } from '../router/authDestination';
import { buildAuthFlowQuery } from '../router/authFlowQuery';
import { useAuthStore } from '../store/auth';
import { AuthRequestError } from '../utils/authError';
import BrandMark from './brand/BrandMark.vue';

const form = ref({
  username: '',
  password: '',
});
const submitting = ref(false);
const formError = ref('');

const authStore = useAuthStore();
const route = useRoute();
const router = useRouter();
const authFlowQuery = computed(() => buildAuthFlowQuery(route.query));
const loginDestination = computed(() =>
  resolveAuthSuccessDestination(router, route.query, authStore.currentIdentity),
);

const formatLoginError = (error: unknown) => {
  if (error instanceof AuthRequestError && error.code === 'AUTH_REQUEST_TIMEOUT') {
    return 'Request timed out. Check your connection and try again.';
  }

  const message = error instanceof Error ? error.message : '';
  if (message === 'Invalid username or password') {
    return 'Invalid username or password.';
  }
  return 'Could not log in. Please try again.';
};

const login = async () => {
  if (submitting.value) {
    return;
  }

  formError.value = '';
  if (form.value.username.length === 0) {
    formError.value = 'Enter your username.';
    return;
  }
  if (form.value.password.length === 0) {
    formError.value = 'Enter your password.';
    return;
  }

  submitting.value = true;
  try {
    await authStore.login(form.value.username, form.value.password);
    void router.replace(loginDestination.value);
  } catch (error) {
    formError.value = formatLoginError(error);
  } finally {
    submitting.value = false;
  }
};
</script>
