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
import { onBeforeUnmount, onMounted, watch } from 'vue';
import { useRoute } from 'vue-router';
import { useAuthStore } from '../../store/auth';
import { useNotificationStore } from '../../store/notification';
import { usePostSearchSessionStore } from '../../store/postSearchSession';
import { usePostPublishStore } from '../../store/postPublish';
import { useReplySubmissionStore } from '../../store/replySubmission';
import { useSearchSessionStore } from '../../store/searchSession';
import LeftSidebar from './LeftSidebar.vue';
import MobileBottomNav from './MobileBottomNav.vue';
import RightRail from './RightRail.vue';

const NOTIFICATION_POLL_INTERVAL_MS = 60_000;

const authStore = useAuthStore();
const route = useRoute();
const notificationStore = useNotificationStore();
const postSearchSession = usePostSearchSessionStore();
const postPublishStore = usePostPublishStore();
const replySubmissionStore = useReplySubmissionStore();
const searchSession = useSearchSessionStore();
let notificationPollTimer: number | null = null;
let disposed = false;
let notificationPollGeneration = 0;
let notificationFailureStreak = 0;

const notificationPollDelay = () => notificationFailureStreak === 0
  ? NOTIFICATION_POLL_INTERVAL_MS
  : Math.min(15 * 60_000, NOTIFICATION_POLL_INTERVAL_MS * 2 ** notificationFailureStreak * (0.8 + Math.random() * 0.4));

const currentViewerID = () => (
  authStore.isAuthenticated ? authStore.currentIdentity?.id ?? null : null
);

const canPollNotifications = () => (
  !disposed
  && authStore.isAuthenticated
  && notificationStore.captureViewer() !== null
  && document.visibilityState === 'visible'
);

const clearNotificationPollTimer = () => {
  if (notificationPollTimer === null) {
    return;
  }
  window.clearTimeout(notificationPollTimer);
  notificationPollTimer = null;
};

const refreshUnreadAndMaybeRevalidate = async () => {
  const capture = notificationStore.captureViewer();
  if (!capture) {
    return false;
  }
  try {
    await notificationStore.refreshUnreadCount(capture);
    if (route.name === 'Notifications' && notificationStore.listStale) {
      await notificationStore.revalidateNotifications();
    }
    return true;
  } catch {
    // Background freshness failures keep the cached badge and list unchanged.
    return false;
  }
};

const scheduleNotificationPoll = () => {
  clearNotificationPollTimer();
  if (!canPollNotifications()) {
    return;
  }

  notificationPollTimer = window.setTimeout(() => {
    notificationPollTimer = null;
    void runScheduledNotificationRefresh();
  }, notificationPollDelay());
};

const finishNotificationRefresh = (generation: number, succeeded: boolean) => {
  if (disposed || generation !== notificationPollGeneration) return;
  notificationFailureStreak = succeeded ? 0 : Math.min(5, notificationFailureStreak + 1);
  scheduleNotificationPoll();
};

const runScheduledNotificationRefresh = async () => {
  if (!canPollNotifications()) {
    return;
  }
  const generation = notificationPollGeneration;
  const succeeded = await refreshUnreadAndMaybeRevalidate();
  finishNotificationRefresh(generation, succeeded);
};

const refreshNowAndRestartNotificationPoll = (allowHidden = false) => {
  clearNotificationPollTimer();
  const generation = ++notificationPollGeneration;
  if (disposed || !authStore.isAuthenticated) {
    return;
  }
  if (!allowHidden && document.visibilityState !== 'visible') {
    return;
  }

  void refreshUnreadAndMaybeRevalidate().then(succeeded => finishNotificationRefresh(generation, succeeded));
};

const syncSessionViewers = () => {
  const nextViewerID = currentViewerID();
  notificationPollGeneration += 1;
  notificationFailureStreak = 0;
  notificationStore.setViewer(nextViewerID);
  searchSession.setViewer(nextViewerID);
  postSearchSession.setViewer(nextViewerID);
  void postPublishStore.activateViewer(nextViewerID);
  void replySubmissionStore.activateViewer(nextViewerID).catch(() => undefined);
  if (nextViewerID === null) {
    clearNotificationPollTimer();
    return;
  }
  refreshNowAndRestartNotificationPoll(true);
};

// AppShell is the sole unread coordinator. Identity changes invalidate old
// requests; access-token rotation for the same identity does not refetch.
watch(() => currentViewerID(), syncSessionViewers, { immediate: true });
watch(() => route.name, (name) => {
  if (name === 'Notifications' && authStore.isAuthenticated) {
    refreshNowAndRestartNotificationPoll(true);
  }
});

const handleVisibilityChange = () => {
  if (document.visibilityState === 'visible') {
    refreshNowAndRestartNotificationPoll();
  } else {
    notificationPollGeneration += 1;
    clearNotificationPollTimer();
  }
};

const handleOnline = () => {
  refreshNowAndRestartNotificationPoll();
};

const handlePageShow = () => {
  refreshNowAndRestartNotificationPoll();
};

onMounted(() => {
  document.addEventListener('visibilitychange', handleVisibilityChange);
  window.addEventListener('online', handleOnline);
  window.addEventListener('pageshow', handlePageShow);
});

onBeforeUnmount(() => {
  disposed = true;
  notificationPollGeneration += 1;
  clearNotificationPollTimer();
  document.removeEventListener('visibilitychange', handleVisibilityChange);
  window.removeEventListener('online', handleOnline);
  window.removeEventListener('pageshow', handlePageShow);
});
</script>

<style scoped>
.app-layout {
  display: grid;
  width: min(
    100%,
    calc(
      var(--shell-left-width)
      + var(--shell-main-width)
      + var(--shell-right-width)
      + var(--space-6)
      + var(--space-6)
    )
  );
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
    width: min(100%, calc(96px + var(--shell-main-width) + var(--space-4)));
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

  .app-layout__left :deep(.left-sidebar__link .app-icon) {
    width: 24px;
    height: 24px;
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
    width: 22px;
    height: 22px;
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
