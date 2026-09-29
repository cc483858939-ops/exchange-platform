<template>
  <main class="auth-page">
    <div class="auth-layout">
      <section class="auth-content" aria-labelledby="register-title">
        <div class="auth-brand">
          <span class="auth-brand__mobile-mark" aria-hidden="true"><BrandMark /></span>
          <span class="auth-brand__name">Exchange</span>
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
        <RouterLink :to="{ name: 'Login', query: authFlowQuery }">Log in</RouterLink>
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
const registerDestination = computed(() =>
  resolveAuthSuccessDestination(router, route.query, authStore.currentIdentity),
);

const formatRegisterError = (error: unknown) => {
  if (error instanceof AuthRequestError && error.code === 'AUTH_REQUEST_TIMEOUT') {
    return 'Request timed out. Check your connection and try again.';
  }
  if (error instanceof AuthRequestError && error.code === 'AUTH_USERNAME_UNAVAILABLE') {
    return 'That username is already in use. Choose another username.';
  }
  if (error instanceof AuthRequestError && error.code === 'AUTH_REQUEST_INVALID') {
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
    void router.replace(registerDestination.value);
  } catch (error) {
    formError.value = formatRegisterError(error);
  } finally {
    submitting.value = false;
  }
};
</script>

<style scoped src="../styles/auth-shell.css"></style>
