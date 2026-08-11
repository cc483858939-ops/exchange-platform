<template>
  <div class="left-sidebar">
    <router-link class="left-sidebar__brand" :to="{ name: 'Home' }" aria-label="Go Exchange home">
      <span class="left-sidebar__brand-mark" aria-hidden="true">GX</span>
      <span class="left-sidebar__brand-name">Go Exchange</span>
    </router-link>

    <nav class="left-sidebar__nav" aria-label="Main navigation">
      <router-link
        v-for="item in navigation"
        :key="item.name"
        class="left-sidebar__link"
        :class="{
          'left-sidebar__link--wide-exchange': item.name === 'CurrencyExchange',
        }"
        :to="{ name: item.name }"
      >
        {{ item.label }}
      </router-link>
      <router-link
        v-if="authStore.isAuthenticated && currentProfileID !== null"
        class="left-sidebar__link"
        :to="{
          name: 'UserProfile',
          params: { id: String(currentProfileID) },
        }"
      >
        Profile
      </router-link>
      <router-link
        v-if="authStore.isAuthenticated"
        class="left-sidebar__link"
        :to="{ name: 'ArticleCreate' }"
      >
        Post
      </router-link>
    </nav>

    <div class="left-sidebar__account">
      <template v-if="authStore.isAuthenticated">
        <button class="left-sidebar__link left-sidebar__logout" type="button" @click="handleLogout">
          Log out
        </button>
      </template>
      <template v-else>
        <router-link class="left-sidebar__link" :to="{ name: 'Login' }">Log in</router-link>
        <router-link class="left-sidebar__link left-sidebar__signup" :to="{ name: 'Register' }">Sign up</router-link>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useLogout } from '../../composables/useLogout';

const { authStore, handleLogout } = useLogout();
const currentProfileID = computed(() => {
  const id = authStore.currentIdentity?.id;

  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});

const navigation = [
  { name: 'Home', label: 'Home' },
  { name: 'CurrencyExchange', label: 'Exchange' },
];

</script>

<style scoped>
.left-sidebar {
  display: flex;
  min-height: 100%;
  flex-direction: column;
  padding: var(--space-3) 0;
}

.left-sidebar__brand {
  display: inline-flex;
  align-items: center;
  min-height: 48px;
  gap: var(--space-3);
  padding: 0 var(--space-4);
  color: var(--color-text);
  font-size: 16px;
  font-weight: 750;
  text-decoration: none;
}

.left-sidebar__brand-mark {
  display: grid;
  width: 34px;
  height: 34px;
  place-items: center;
  border: 1px solid var(--color-text);
  border-radius: 50%;
  font-size: 12px;
  font-weight: 800;
  letter-spacing: 0.04em;
}

.left-sidebar__nav {
  display: grid;
  gap: var(--space-1);
  margin-top: var(--space-5);
}

.left-sidebar__link {
  display: flex;
  align-items: center;
  min-height: 44px;
  margin: 0 var(--space-2);
  padding: 0 var(--space-3);
  border: 0;
  border-left: 3px solid transparent;
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
  font: inherit;
  font-size: 15px;
  font-weight: 620;
  text-align: left;
  text-decoration: none;
  transition: background-color var(--transition-fast), color var(--transition-fast), border-color var(--transition-fast);
}

.left-sidebar__link:hover,
.left-sidebar__link:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-text);
}

.left-sidebar__link.router-link-active {
  border-left-color: var(--color-accent);
  color: var(--color-text);
  font-weight: 750;
}

.left-sidebar__account {
  display: grid;
  gap: var(--space-1);
  margin-top: auto;
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border);
}

.left-sidebar__signup {
  color: var(--color-accent);
}

@media (min-width: 1280px) {
  .left-sidebar__link--wide-exchange {
    display: none;
  }
}

.left-sidebar__logout {
  width: calc(100% - var(--space-4));
}
</style>
