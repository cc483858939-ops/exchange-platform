<template>
  <div class="app-layout">
    <aside class="app-layout__left">
      <LeftSidebar :notification-badge="notificationStore.unreadBadge" />
    </aside>

    <main class="app-layout__main">
      <slot />
    </main>

    <aside class="app-layout__right">
      <RightRail />
    </aside>

    <MobileBottomNav :notification-badge="notificationStore.unreadBadge" />
  </div>
</template>

<script setup lang="ts">
import { watch } from 'vue';
import { useRoute } from 'vue-router';
import { useAuthStore } from '../../store/auth';
import { useNotificationStore } from '../../store/notification';
import LeftSidebar from './LeftSidebar.vue';
import MobileBottomNav from './MobileBottomNav.vue';
import RightRail from './RightRail.vue';

const authStore = useAuthStore();
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
    display: block;
    width: 100%;
    min-height: 100vh;
    min-height: 100dvh;
  }

  .app-layout__left,
  .app-layout__right {
    display: none;
  }

  .app-layout__main {
    box-sizing: border-box;
    min-height: 100vh;
    min-height: 100dvh;
    overflow: clip;
    border-inline: 0;
    padding-top: var(--mobile-safe-top);
    padding-bottom: calc(
      var(--mobile-bottom-nav-height)
      + var(--mobile-safe-bottom)
    );
  }
}
</style>
