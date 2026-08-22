<template>
  <div class="app-layout">
    <aside class="app-layout__left">
      <LeftSidebar :notification-badge="notificationStore.unreadBadge" />
    </aside>

    <header class="app-layout__mobile-nav">
      <div class="app-layout__mobile-header">
        <router-link class="app-layout__mobile-brand" :to="{ name: 'Home' }" aria-label="Go Exchange home">
          GX
        </router-link>
        <div class="app-layout__mobile-account">
          <template v-if="authStore.isAuthenticated">
            <router-link :to="{ name: 'ArticleCreate' }" aria-label="Post" title="Post">
              <AppIcon name="compose" :size="20" />
              <span>Post</span>
            </router-link>
            <button type="button" aria-label="Log out" title="Log out" @click="handleLogout">
              <AppIcon name="logout" :size="20" />
              <span>Log out</span>
            </button>
          </template>
          <template v-else>
            <router-link :to="{ name: 'Login' }">Log in</router-link>
            <router-link class="app-layout__mobile-signup" :to="{ name: 'Register' }">Sign up</router-link>
          </template>
        </div>
      </div>
      <nav class="app-layout__mobile-links" :class="{ 'app-layout__mobile-links--authenticated': authStore.isAuthenticated }" aria-label="Mobile navigation">
        <router-link :to="{ name: 'Home' }" aria-label="Home" title="Home">
          <AppIcon name="home" :size="20" />
          <span>Home</span>
        </router-link>
        <router-link v-if="authStore.isAuthenticated" :to="{ name: 'UserSearch' }" aria-label="Search" title="Search">
          <AppIcon name="search" :size="20" />
          <span>Search</span>
        </router-link>
        <router-link v-if="authStore.isAuthenticated" class="app-layout__notification-link" :to="{ name: 'Notifications' }" aria-label="Notifications" title="Notifications">
          <AppIcon name="notifications" :size="20" />
          <span>Notifications</span>
          <span v-if="notificationStore.unreadBadge" class="app-layout__notification-badge">{{ notificationStore.unreadBadge }}</span>
        </router-link>
        <router-link v-if="authStore.isAuthenticated" :to="{ name: 'History' }" aria-label="History" title="History">
          <AppIcon name="history" :size="20" />
          <span>History</span>
        </router-link>
        <router-link :to="{ name: 'CurrencyExchange' }" aria-label="Exchange" title="Exchange">
          <AppIcon name="exchange" :size="20" />
          <span>Exchange</span>
        </router-link>
      </nav>
    </header>

    <main class="app-layout__main">
      <slot />
    </main>

    <aside class="app-layout__right">
      <RightRail />
    </aside>
  </div>
</template>

<script setup lang="ts">
import { watch } from 'vue';
import { useRoute } from 'vue-router';
import { useLogout } from '../../composables/useLogout';
import { useNotificationStore } from '../../store/notification';
import AppIcon from '../icons/AppIcon.vue';
import LeftSidebar from './LeftSidebar.vue';
import RightRail from './RightRail.vue';

const { authStore, handleLogout } = useLogout();
const route = useRoute();
const notificationStore = useNotificationStore();

const syncNotificationViewer = () => {
  const nextViewerID = authStore.isAuthenticated ? authStore.currentIdentity?.id ?? null : null;
  notificationStore.setViewer(nextViewerID);
  const capture = notificationStore.captureViewer();
  if (capture) {
    void notificationStore.refreshUnreadCount(capture).catch(() => undefined);
  }
};

// AppShell is the sole unread coordinator. Identity changes invalidate old
// requests; access-token rotation for the same identity does not refetch.
watch(() => authStore.currentIdentity?.id, syncNotificationViewer, { immediate: true });
watch(() => route.name, (name) => {
  if (name === 'Notifications' && authStore.isAuthenticated) {
    const capture = notificationStore.captureViewer();
    if (capture) {
      void notificationStore.refreshUnreadCount(capture).catch(() => undefined);
    }
  }
});
</script>

<style scoped>
.app-layout {
  --app-mobile-nav-offset: 0px;
  display: grid;
  width: min(100%, 1268px);
  min-height: 100vh;
  grid-template-columns: var(--shell-left-width) minmax(0, var(--shell-main-width)) var(--shell-right-width);
  align-items: start;
  gap: var(--space-6);
  margin: 0 auto;
}

.app-layout__left,
.app-layout__right {
  position: sticky;
  top: 0;
  height: 100vh;
  min-width: 0;
}

.app-layout__left {
  overflow-y: auto;
}

.app-layout__main {
  min-width: 0;
  min-height: 100vh;
  overflow: clip;
  border-inline: 1px solid var(--color-border);
  background: var(--color-surface);
}

.app-layout__mobile-nav {
  display: none;
}

@media (max-width: 1279px) {
  .app-layout {
    width: min(100%, 792px);
    grid-template-columns: 96px minmax(0, var(--shell-main-width));
    gap: var(--space-4);
  }

  .app-layout__right {
    display: none;
  }

  .app-layout__left :deep(.left-sidebar__brand) {
    justify-content: center;
    padding-inline: var(--space-2);
  }

  .app-layout__left :deep(.left-sidebar__brand-name) {
    display: none;
  }

  .app-layout__left :deep(.left-sidebar__link) {
    justify-content: center;
    min-height: 48px;
    margin-inline: var(--space-1);
    padding-inline: var(--space-1);
    font-size: 12px;
    text-align: center;
  }

  .app-layout__left :deep(.left-sidebar__link--icon) {
    gap: 0;
  }

  .app-layout__left :deep(.left-sidebar__link--icon .left-sidebar__label) {
    display: none;
  }

  .app-layout__left :deep(.left-sidebar__link--primary) {
    width: 48px;
    min-height: 48px;
    margin-inline: auto;
    padding-inline: 0;
  }

  .app-layout__left :deep(.left-sidebar__link--primary .app-icon) {
    color: var(--color-surface);
  }
}

@media (max-width: 799px) {
  .app-layout {
    --app-mobile-nav-offset: calc(
      var(--space-2) + 32px + var(--space-2) + 34px + var(--space-3) + 1px
    );
    display: block;
    width: 100%;
  }

  .app-layout__left,
  .app-layout__right {
    display: none;
  }

  .app-layout__mobile-nav {
    position: sticky;
    top: 0;
    z-index: 20;
    display: grid;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-3) var(--space-3);
    border-bottom: 1px solid var(--color-border);
    background: color-mix(in srgb, var(--color-surface) 94%, transparent);
    backdrop-filter: blur(10px);
  }

  .app-layout__mobile-header,
  .app-layout__mobile-account {
    display: flex;
    align-items: center;
  }

  .app-layout__mobile-header {
    justify-content: space-between;
    gap: var(--space-3);
  }

  .app-layout__mobile-brand {
    display: grid;
    width: 32px;
    height: 32px;
    flex: 0 0 auto;
    place-items: center;
    border: 1px solid var(--color-text);
    border-radius: 50%;
    color: var(--color-text);
    font-size: 11px;
    font-weight: 800;
    text-decoration: none;
  }

  .app-layout__mobile-account {
    min-width: 0;
    gap: 5px;
  }

  .app-layout__mobile-account a,
  .app-layout__mobile-account button {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    min-height: 32px;
    border: 0;
    padding: 0;
    background: transparent;
    color: var(--color-text-secondary);
    font: inherit;
    font-size: 13px;
    font-weight: 650;
    text-decoration: none;
    white-space: nowrap;
  }

  .app-layout__mobile-account .app-layout__mobile-signup {
    color: var(--color-accent);
  }

  .app-layout__mobile-links {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-1);
  }

  .app-layout__mobile-links--authenticated {
    grid-template-columns: repeat(5, minmax(0, 1fr));
  }

  .app-layout__mobile-links a {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    min-width: 0;
    min-height: 34px;
    overflow: hidden;
    border-bottom: 2px solid transparent;
    color: var(--color-text-secondary);
    font-size: 12px;
    font-weight: 650;
    text-align: center;
    text-decoration: none;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .app-layout__mobile-links--authenticated a {
    flex-direction: column;
    gap: 1px;
    min-height: 44px;
    padding-inline: 0;
    font-size: 10px;
    line-height: 1.1;
  }

  .app-layout__mobile-links--authenticated a .app-icon {
    width: 18px;
    height: 18px;
  }

  .app-layout__notification-link {
    position: relative;
  }

  .app-layout__notification-badge {
    position: absolute;
    top: 0;
    right: 2px;
    min-width: 16px;
    height: 16px;
    padding: 0 3px;
    border-radius: var(--radius-pill);
    background: var(--color-accent);
    color: var(--color-surface);
    font-size: 9px;
    font-weight: 800;
    line-height: 16px;
  }

  .app-layout__mobile-links a.router-link-active {
    border-bottom-color: var(--color-accent);
    color: var(--color-text);
  }

  .app-layout__main {
    min-height: calc(100vh - 102px);
    overflow: clip;
    border-inline: 0;
  }
}

@media (max-width: 360px) {
  .app-layout__mobile-account {
    gap: 4px;
  }

  .app-layout__mobile-account a,
  .app-layout__mobile-account button {
    gap: 4px;
    font-size: 12px;
  }

  .app-layout__mobile-links:not(.app-layout__mobile-links--authenticated) a {
    gap: 4px;
    font-size: 11px;
  }

  .app-layout__mobile-links--authenticated a {
    font-size: 9px;
    letter-spacing: -0.02em;
  }
}
</style>
