<template>
  <section
    class="auth-required-state"
    aria-labelledby="auth-required-title"
    aria-live="polite"
  >
    <h1 id="auth-required-title">{{ title }}</h1>
    <p>{{ description }}</p>
    <RouterLink class="auth-required-state__action" :to="loginDestination">
      Log in
    </RouterLink>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue';

const props = defineProps<{
  title: string;
  description: string;
  returnTo: string;
}>();

const loginDestination = computed(() => ({
  name: 'Login',
  query: {
    returnTo: props.returnTo,
  },
}));
</script>

<style scoped>
.auth-required-state {
  display: grid;
  min-height: 260px;
  place-content: center;
  justify-items: center;
  gap: var(--space-3);
  padding: clamp(56px, 12vw, 112px) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background:
    radial-gradient(circle at 50% 0%, color-mix(in srgb, var(--color-accent) 9%, transparent), transparent 42%),
    var(--color-surface);
  text-align: center;
}

.auth-required-state h1 {
  max-width: 28rem;
  margin: 0;
  color: var(--color-text);
  font-size: clamp(24px, 4vw, 32px);
  line-height: 1.12;
  letter-spacing: -0.03em;
}

.auth-required-state p {
  max-width: 34rem;
  margin: 0;
  color: var(--color-text-secondary);
  line-height: 1.5;
}

.auth-required-state__action {
  display: inline-flex;
  min-height: 40px;
  align-items: center;
  justify-content: center;
  margin-top: var(--space-2);
  border: 1px solid var(--color-text);
  border-radius: var(--radius-pill);
  padding: 0 var(--space-5);
  background: var(--color-text);
  color: var(--color-surface);
  font-size: 14px;
  font-weight: 750;
  text-decoration: none;
  transition: background var(--transition-fast), border-color var(--transition-fast), color var(--transition-fast), transform var(--transition-fast);
}

.auth-required-state__action:hover,
.auth-required-state__action:focus-visible {
  border-color: var(--color-accent);
  background: var(--color-accent);
  color: #fff;
  transform: translateY(-1px);
}

@media (max-width: 420px) {
  .auth-required-state {
    padding-inline: var(--space-4);
  }
}
</style>
