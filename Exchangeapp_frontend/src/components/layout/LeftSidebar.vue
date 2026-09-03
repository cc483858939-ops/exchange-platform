<template>
  <div class="left-sidebar">
    <router-link class="left-sidebar__brand" :to="{ name: 'Home' }" aria-label="Go Exchange home">
      <span class="left-sidebar__brand-mark" aria-hidden="true">GX</span>
      <span class="left-sidebar__brand-name">Go Exchange</span>
    </router-link>

    <nav class="left-sidebar__nav" aria-label="Main navigation">
      <router-link
        v-for="item in visibleNavigation"
        :key="item.name"
        class="left-sidebar__link left-sidebar__link--icon"
        :class="{
          'left-sidebar__link--compact-only': item.compactOnly,
        }"
        :to="{ name: item.name }"
        :aria-label="item.label"
        :title="item.label"
      >
        <AppIcon :name="item.icon" :size="24" />
        <span class="left-sidebar__label">{{ item.label }}</span>
        <span
          v-if="item.name === 'Notifications' && notificationBadge"
          class="left-sidebar__badge"
          aria-label="Unread notifications"
        >{{ notificationBadge }}</span>
      </router-link>
      <router-link
        v-if="authStore.isAuthenticated && currentProfileID !== null"
        class="left-sidebar__link left-sidebar__link--icon"
        :to="{
          name: 'UserProfile',
          params: { id: String(currentProfileID) },
        }"
        aria-label="Profile"
        title="Profile"
      >
        <AppIcon name="profile" :size="24" />
        <span class="left-sidebar__label">Profile</span>
      </router-link>
      <router-link
        v-if="authStore.isAuthenticated"
        class="left-sidebar__link left-sidebar__link--icon left-sidebar__link--primary"
        :to="{ name: 'PostCreate' }"
        aria-label="Post"
        title="Post"
      >
        <AppIcon name="compose" :size="22" />
        <span class="left-sidebar__label">Post</span>
      </router-link>
    </nav>

    <div class="left-sidebar__account">
      <template v-if="authStore.isAuthenticated">
        <button
          class="left-sidebar__link left-sidebar__link--icon left-sidebar__logout"
          type="button"
          aria-label="Log out"
          title="Log out"
          @click="handleLogout"
        >
          <AppIcon name="logout" :size="24" />
          <span class="left-sidebar__label">Log out</span>
        </button>
      </template>
      <template v-else>
        <router-link class="left-sidebar__link" :to="{ name: 'Login' }">
          <span class="left-sidebar__label">Log in</span>
        </router-link>
        <router-link class="left-sidebar__link left-sidebar__signup" :to="{ name: 'Register' }">
          <span class="left-sidebar__label">Sign up</span>
        </router-link>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useLogout } from '../../composables/useLogout';
import AppIcon from '../icons/AppIcon.vue';

withDefaults(defineProps<{ notificationBadge?: string | null }>(), {
  notificationBadge: null,
});

const { authStore, handleLogout } = useLogout();
const currentProfileID = computed(() => {
  const id = authStore.currentIdentity?.id;

  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});

const navigation = [
  { name: 'Home', label: 'Home', icon: 'home' as const, compactOnly: false, authOnly: false },
  { name: 'UserSearch', label: 'Search', icon: 'search' as const, compactOnly: false, authOnly: true },
  { name: 'Notifications', label: 'Notifications', icon: 'notifications' as const, compactOnly: false, authOnly: true },
  { name: 'History', label: 'History', icon: 'history' as const, compactOnly: false, authOnly: true },
  { name: 'CurrencyExchange', label: 'Exchange', icon: 'exchange' as const, compactOnly: true, authOnly: false },
];

const visibleNavigation = computed(() => navigation.filter((item) => !item.authOnly || authStore.isAuthenticated));

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
  min-height: 50px;
  gap: 14px;
  margin: 0 var(--space-2);
  padding: 0 var(--space-4);
  border: 0;
  border-radius: var(--radius-pill);
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

.left-sidebar__link--compact-only {
  display: none;
}

.left-sidebar__link:hover,
.left-sidebar__link:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-text);
}

.left-sidebar__link.router-link-active {
  color: var(--color-text);
  font-weight: 750;
}

.left-sidebar__link.router-link-active .app-icon {
  color: var(--color-accent);
}

.left-sidebar__badge {
  display: inline-flex;
  min-width: 22px;
  height: 20px;
  align-items: center;
  justify-content: center;
  margin-left: auto;
  padding: 0 6px;
  border-radius: var(--radius-pill);
  background: var(--color-accent);
  color: var(--color-surface);
  font-size: 11px;
  font-weight: 800;
  line-height: 1;
}

.left-sidebar__link--primary {
  min-height: 48px;
  margin-top: var(--space-2);
  justify-content: center;
  background: var(--color-accent);
  color: var(--color-surface);
  font-weight: 750;
}

.left-sidebar__link--primary:hover,
.left-sidebar__link--primary:focus-visible {
  background: var(--color-accent-hover);
  color: var(--color-surface);
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

.left-sidebar__logout {
  width: auto;
}

@media (min-width: 800px) and (max-width: 1279px) {
  .left-sidebar__link--compact-only {
    display: flex;
  }
}
</style>
