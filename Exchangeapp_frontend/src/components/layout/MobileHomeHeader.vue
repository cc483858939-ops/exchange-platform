<template>
  <div class="mobile-home-header">
    <RouterLink
      v-if="authStore.isAuthenticated && identity"
      class="mobile-home-header__profile"
      :to="{ name: 'UserProfile', params: { id: String(identity.id) } }"
      aria-label="Profile"
    >
      <UserAvatar
        class="mobile-home-header__avatar"
        :avatar-url="identity.avatar_url"
        :display-name="identity.display_name"
        :username="identity.username"
        :size="36"
        decorative
      />
    </RouterLink>
    <RouterLink
      v-else
      class="mobile-home-header__profile"
      :to="{ name: 'Login' }"
      aria-label="Log in"
    >
      <span class="mobile-home-header__avatar mobile-home-header__avatar--anonymous">
        <AppIcon name="profile" :size="22" />
      </span>
    </RouterLink>

    <RouterLink class="mobile-home-header__brand" :to="{ name: 'Home' }" aria-label="Exchange home">
      EX
    </RouterLink>

    <span class="mobile-home-header__spacer" aria-hidden="true"></span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useAuthStore } from '../../store/auth';
import AppIcon from '../icons/AppIcon.vue';
import UserAvatar from '../users/UserAvatar.vue';

const authStore = useAuthStore();
const identity = computed(() => authStore.currentIdentity);
</script>

<style scoped>
.mobile-home-header {
  display: none;
}

@media (max-width: 799px) {
  .mobile-home-header {
    display: grid;
    min-width: 0;
    height: var(--mobile-topbar-height);
    grid-template-columns: 44px minmax(0, 1fr) 44px;
    align-items: center;
    padding-inline: var(--space-3);
  }

  .mobile-home-header__profile,
  .mobile-home-header__brand,
  .mobile-home-header__spacer {
    display: grid;
    width: 44px;
    height: 44px;
    place-items: center;
  }

  .mobile-home-header__profile,
  .mobile-home-header__brand {
    color: var(--color-text);
    text-decoration: none;
  }

  .mobile-home-header__profile:focus-visible,
  .mobile-home-header__brand:focus-visible {
    border-radius: var(--radius-pill);
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }

  .mobile-home-header__avatar {
    position: relative;
    display: grid;
    width: 36px;
    height: 36px;
    place-items: center;
    overflow: hidden;
    border: 1px solid var(--color-border-strong);
    border-radius: 50%;
    background: var(--color-surface-subtle);
    color: var(--color-text-secondary);
    font-size: 15px;
    font-weight: 800;
  }

  .mobile-home-header__avatar--anonymous {
    border-color: var(--color-border);
  }

  .mobile-home-header__avatar img {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
  }

  .mobile-home-header__brand {
    justify-self: center;
    font-size: 22px;
    font-weight: 800;
    letter-spacing: -0.03em;
  }
}
</style>
