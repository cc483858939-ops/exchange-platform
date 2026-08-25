<template>
  <div class="mobile-home-header">
    <RouterLink
      v-if="authStore.isAuthenticated && identity"
      class="mobile-home-header__profile"
      :to="{ name: 'UserProfile', params: { id: String(identity.id) } }"
      aria-label="Profile"
    >
      <span class="mobile-home-header__avatar">
        <span v-if="avatarInitial" class="mobile-home-header__avatar-fallback" aria-hidden="true">
          {{ avatarInitial }}
        </span>
        <AppIcon v-else name="profile" :size="22" />
        <img
          v-if="avatarSource"
          :src="avatarSource"
          alt=""
          v-show="avatarLoaded"
          @load="avatarLoaded = true"
          @error="avatarLoadFailed = true; avatarLoaded = false"
        />
      </span>
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
import { computed, ref, watch } from 'vue';
import { useAuthStore } from '../../store/auth';
import AppIcon from '../icons/AppIcon.vue';

const authStore = useAuthStore();
const avatarLoadFailed = ref(false);
const avatarLoaded = ref(false);
const identity = computed(() => authStore.currentIdentity);

const avatarSource = computed(() => {
  const value = identity.value?.avatar_url?.trim() || '';
  return value && !avatarLoadFailed.value ? value : '';
});

const avatarInitial = computed(() => {
  const value = identity.value?.display_name?.trim()
    || identity.value?.username?.trim()
    || '';
  return Array.from(value)[0]?.toUpperCase() || '';
});

watch(
  [() => identity.value?.id, () => identity.value?.avatar_url],
  () => {
    avatarLoadFailed.value = false;
    avatarLoaded.value = false;
  },
);
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

  .mobile-home-header__avatar-fallback {
    display: grid;
    width: 100%;
    height: 100%;
    place-items: center;
  }

  .mobile-home-header__brand {
    justify-self: center;
    font-size: 22px;
    font-weight: 800;
    letter-spacing: -0.03em;
  }
}
</style>
