<template>
  <section class="notifications-page" aria-labelledby="notifications-title">
    <div
      ref="notificationsScrollViewportRef"
      class="notifications-scroll-viewport"
    >
      <header class="notifications-page__header">
        <div>
          <p class="notifications-page__eyebrow">INBOX</p>
          <h1 id="notifications-title">Notifications</h1>
        </div>
        <button
          v-if="hasUnread && !markAllPending"
          class="notifications-page__mark-all"
          type="button"
          @click="markAll"
        >
          Mark all as read
        </button>
        <span v-else-if="markAllPending" class="notifications-page__pending-label">Updating…</span>
      </header>

      <div v-if="!authStore.isAuthenticated" class="notifications-page__state">
        <h2>Log in to view your notifications.</h2>
        <router-link class="notifications-page__action" :to="{ name: 'Login' }">Log in</router-link>
      </div>
      <div v-else-if="loading && items.length === 0" class="notifications-page__state" aria-live="polite">
        <p>Loading notifications…</p>
      </div>
      <div v-else-if="error && items.length === 0" class="notifications-page__state notifications-page__state--error" role="alert">
        <p>We couldn’t load your notifications.</p>
        <button class="notifications-page__action" type="button" @click="loadInitial">Try again</button>
      </div>
      <div v-else-if="items.length === 0" class="notifications-page__state">
        <span class="notifications-page__empty-icon" aria-hidden="true">
          <AppIcon name="notifications" :size="28" />
        </span>
        <h2>You’re all caught up.</h2>
        <p>New likes, replies, and follows will appear here.</p>
      </div>
      <div v-else class="notifications-page__list" aria-live="polite">
        <article
          v-for="item in items"
          :key="item.id"
          class="notification-card"
          :class="{ 'notification-card--unread': !item.read }"
        >
          <button class="notification-card__open" type="button" @click="openNotification(item)">
            <UserAvatar
              class="notification-card__avatar"
              :avatar-url="item.actor.avatar_url"
              :display-name="item.actor.display_name"
              :username="item.actor.username"
              :size="44"
              decorative
            />
            <span class="notification-card__body">
              <span class="notification-card__title">
                <strong>{{ item.actor.display_name || item.actor.username }}</strong>
                {{ notificationCopy(item) }}
              </span>
              <span class="notification-card__meta">{{ formatActivityAt(item.activity_at) }}</span>
            </span>
            <span v-if="!item.read" class="notification-card__dot" aria-label="Unread" />
            <span v-if="pendingReadIDs.has(item.id)" class="notification-card__pending" aria-label="Saving">…</span>
          </button>
        </article>
        <div ref="sentinel" class="notifications-page__sentinel" aria-hidden="true" />
        <div v-if="loadingMore" class="notifications-page__load-state" aria-live="polite">Loading more…</div>
        <div v-if="loadMoreError" class="notifications-page__load-state notifications-page__load-state--error" role="alert">
          <span>Couldn’t load more notifications.</span>
          <button class="notifications-page__action" type="button" @click="loadMore">Try again</button>
        </div>
        <button
          v-if="nextCursor && !observerAvailable && !loadingMore"
          class="notifications-page__load-more"
          type="button"
          @click="loadMore"
        >
          Load more
        </button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import {
  computed,
  nextTick,
  onActivated,
  onBeforeUnmount,
  onDeactivated,
  onMounted,
  ref,
  watch,
} from 'vue';
import { storeToRefs } from 'pinia';
import { onBeforeRouteLeave, useRouter } from 'vue-router';
import { useAuthStore } from '../store/auth';
import { useNotificationStore } from '../store/notification';
import AppIcon from '../components/icons/AppIcon.vue';
import UserAvatar from '../components/users/UserAvatar.vue';
import type { Notification } from '../types/Notification';

const NOTIFICATIONS_RESELECT_TOP_THRESHOLD_PX = 8;

const authStore = useAuthStore();
const notificationStore = useNotificationStore();
const router = useRouter();
const {
  items,
  nextCursor,
  loaded,
  loading,
  error,
  loadingMore,
  loadMoreError,
  pendingReadIDs,
  markAllPending,
} = storeToRefs(notificationStore);
const sentinel = ref<HTMLElement | null>(null);
const notificationsScrollViewportRef = ref<HTMLElement | null>(null);
const observerAvailable = ref(typeof IntersectionObserver !== 'undefined');
let observer: IntersectionObserver | null = null;
let mounted = false;
const notificationsViewActive = ref(true);
let resumeOnActivation = false;
let notificationEntryVersion = 0;
let restoredEntryVersion = -1;

const hasUnread = computed(() => items.value.some((item) => !item.read));

const currentViewerID = computed(() => (
  authStore.isAuthenticated ? authStore.currentIdentity?.id ?? null : null
));

const disconnectObserver = () => {
  observer?.disconnect();
  observer = null;
};

const saveCurrentScroll = () => {
  const viewport = notificationsScrollViewportRef.value;
  if (!viewport) {
    return;
  }

  notificationStore.saveScrollTop(viewport.scrollTop);
};

const setupObserver = async () => {
  disconnectObserver();
  if (!mounted || !notificationsViewActive.value) {
    return;
  }
  await nextTick();
  if (
    !mounted
    || !notificationsViewActive.value
    || !observerAvailable.value
    || !notificationsScrollViewportRef.value
    || !nextCursor.value
    || !sentinel.value
    || notificationStore.listStale
    || notificationStore.revalidating
  ) {
    return;
  }
  const root = notificationsScrollViewportRef.value;
  if (!root) {
    return;
  }
  observer = new IntersectionObserver((entries) => {
    if (notificationsViewActive.value && entries.some((entry) => entry.isIntersecting)) {
      void notificationStore.loadMore();
    }
  }, { root, rootMargin: '240px 0px' });
  observer.observe(sentinel.value);
};

const loadInitial = () => { void notificationStore.loadInitial(true); };
const loadMore = () => { void notificationStore.loadMore(); };

const prefersReducedMotion = () => typeof window !== 'undefined'
  && typeof window.matchMedia === 'function'
  && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

const handleNotificationReselect = async () => {
  if (!notificationsViewActive.value) {
    return;
  }

  const viewport = notificationsScrollViewportRef.value;
  if (!viewport) {
    return;
  }

  if (viewport.scrollTop > NOTIFICATIONS_RESELECT_TOP_THRESHOLD_PX) {
    viewport.scrollTo({
      top: 0,
      behavior: prefersReducedMotion() ? 'auto' : 'smooth',
    });
    notificationStore.saveScrollTop(0);
    return;
  }

  if (!authStore.isAuthenticated) {
    return;
  }

  await notificationStore.refreshNotifications();
};

const restoreScrollOnce = async () => {
  const entryVersion = notificationEntryVersion;
  if (
    !mounted
    || !notificationsViewActive.value
    || restoredEntryVersion === entryVersion
    || !loaded.value
    || loading.value
  ) {
    return;
  }
  await nextTick();
  if (
    !mounted
    || !notificationsViewActive.value
    || entryVersion !== notificationEntryVersion
    || restoredEntryVersion === entryVersion
  ) {
    return;
  }
  const viewport = notificationsScrollViewportRef.value;
  if (!viewport) {
    return;
  }
  viewport.scrollTop = notificationStore.scrollTop;
  restoredEntryVersion = entryVersion;
};

onBeforeRouteLeave(() => {
  saveCurrentScroll();
});

const openNotification = (item: Notification) => {
  void notificationStore.markNotificationRead(item.id).catch(() => undefined);
  if (item.type === 'user_followed') {
    void router.push({ name: 'UserProfile', params: { id: String(item.actor.id) } });
  } else if (item.post_id !== null) {
    void router.push({ name: 'PostDetail', params: { id: String(item.post_id) } });
  }
};

const markAll = () => { void notificationStore.markAllRead().catch(() => undefined); };

const notificationCopy = (item: Notification) => {
  switch (item.type) {
    case 'post_liked': return 'liked your post.';
    case 'post_replied': return 'replied to your post.';
    case 'user_followed': return 'followed you.';
  }
};

const formatActivityAt = (value: string) => {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return '';
  }
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date);
};

watch(currentViewerID, () => {
  notificationEntryVersion += 1;
  restoredEntryVersion = -1;
  if (notificationsViewActive.value) {
    void notificationStore.loadInitial();
  }
}, { immediate: true });
watch(
  () => notificationStore.notificationReselectVersion,
  () => {
    void handleNotificationReselect();
  },
);
watch([loaded, loading, error], () => { void restoreScrollOnce(); }, { flush: 'post' });
watch([
  nextCursor,
  loadingMore,
  loadMoreError,
  () => items.value.length,
  () => notificationStore.listStale,
  () => notificationStore.revalidating,
], () => { void setupObserver(); }, { flush: 'post' });

onMounted(() => {
  mounted = true;
  void restoreScrollOnce();
});

onDeactivated(() => {
  if (!notificationsViewActive.value) {
    return;
  }

  notificationsViewActive.value = false;
  resumeOnActivation = true;
  disconnectObserver();
});

onActivated(() => {
  if (!resumeOnActivation) {
    return;
  }

  resumeOnActivation = false;
  notificationsViewActive.value = true;

  void notificationStore.loadInitial();

  void nextTick(() => {
    if (!mounted || !notificationsViewActive.value) {
      return;
    }

    void restoreScrollOnce();
    void setupObserver();
  });
});

onBeforeUnmount(() => {
  if (notificationsViewActive.value) {
    saveCurrentScroll();
  }
  mounted = false;
  notificationsViewActive.value = false;
  resumeOnActivation = false;
  disconnectObserver();
});
</script>

<style scoped>
.notifications-page {
  display: flex;
  width: 100%;
  height: 100vh;
  height: 100dvh;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
  background: var(--color-surface);
  color: var(--color-text);
}

.notifications-scroll-viewport {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  box-sizing: border-box;
  overflow-x: hidden;
  overflow-y: auto;
  padding: clamp(24px, 4vw, 48px) clamp(16px, 4vw, 32px) 72px;
  overscroll-behavior-y: contain;
  overflow-anchor: auto;
  -webkit-overflow-scrolling: touch;
}

.notifications-page__header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 22px;
  border-bottom: 1px solid var(--color-border);
}

.notifications-page__eyebrow {
  margin: 0 0 6px;
  color: var(--color-accent);
  font-size: 11px;
  font-weight: 800;
  letter-spacing: 0.16em;
}

.notifications-page h1 {
  margin: 0;
  font-size: clamp(28px, 4vw, 42px);
  letter-spacing: -0.04em;
}

.notifications-page__mark-all,
.notifications-page__load-more,
.notifications-page__action {
  border: 1px solid var(--color-border-strong, var(--color-border));
  border-radius: var(--radius-pill);
  background: var(--color-surface);
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
  font-weight: 700;
  padding: 9px 14px;
  text-decoration: none;
}

.notifications-page__mark-all:hover,
.notifications-page__load-more:hover,
.notifications-page__action:hover {
  border-color: var(--color-accent);
  color: var(--color-accent);
}

.notifications-page__pending-label,
.notifications-page__load-state,
.notifications-page__state {
  color: var(--color-text-secondary);
  font-size: 14px;
}

.notifications-page__state {
  display: grid;
  min-height: 260px;
  place-items: center;
  align-content: center;
  gap: 8px;
  text-align: center;
}

.notifications-page__state h2,
.notifications-page__state p {
  margin: 0;
}

.notifications-page__empty-icon {
  display: grid;
  width: 52px;
  height: 52px;
  place-items: center;
  border-radius: 50%;
  background: var(--color-surface-subtle);
  color: var(--color-text-secondary);
}

.notifications-page__state h2 {
  color: var(--color-text);
  font-size: 20px;
}

.notifications-page__state--error,
.notifications-page__load-state--error {
  color: var(--color-danger, #b42318);
}

.notifications-page__list {
  border-bottom: 1px solid var(--color-border);
}

.notification-card {
  border-bottom: 1px solid var(--color-border);
  background: var(--color-surface);
  transition: background-color 160ms ease;
}

.notification-card--unread {
  background: color-mix(in srgb, var(--color-accent) 5%, var(--color-surface));
}

.notification-card:hover {
  background: var(--color-surface-subtle);
}

.notification-card__open {
  display: grid;
  width: 100%;
  grid-template-columns: 44px minmax(0, 1fr) auto;
  align-items: center;
  gap: 14px;
  border: 0;
  padding: 18px 4px;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font: inherit;
  text-align: left;
}

.notification-card__avatar {
  display: grid;
  width: 44px;
  height: 44px;
  place-items: center;
  overflow: hidden;
  border-radius: 50%;
  background: var(--color-surface-muted, #eef1f4);
  color: var(--color-text-secondary);
  font-weight: 800;
}

.notification-card__avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.notification-card__body {
  display: grid;
  min-width: 0;
  gap: 5px;
}

.notification-card__title {
  overflow: hidden;
  color: var(--color-text);
  font-size: 15px;
  line-height: 1.45;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.notification-card__title strong {
  margin-right: 4px;
}

.notification-card__meta {
  color: var(--color-text-secondary);
  font-size: 12px;
}

.notification-card__dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--color-accent);
}

.notification-card__pending {
  color: var(--color-text-secondary);
  font-weight: 800;
}

.notifications-page__sentinel {
  height: 1px;
}

.notifications-page__load-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 18px 0;
}

.notifications-page__load-more {
  display: block;
  margin: 18px auto 0;
}

@media (max-width: 520px) {
  .notifications-scroll-viewport {
    padding-inline: 14px;
  }

  .notifications-page__header {
    align-items: flex-start;
    flex-direction: column;
  }

  .notification-card__open {
    gap: 10px;
    padding-block: 15px;
  }

  .notification-card__title {
    font-size: 14px;
  }
}

@media (max-width: 799px) {
  .notifications-page {
    height: calc(
      100vh
      - var(--mobile-safe-top)
      - var(--mobile-bottom-nav-height)
      - var(--mobile-safe-bottom)
    );
    height: calc(
      100dvh
      - var(--mobile-safe-top)
      - var(--mobile-bottom-nav-height)
      - var(--mobile-safe-bottom)
    );
  }

  .notifications-scroll-viewport {
    padding: 0 0 24px;
  }

  .notifications-page__header {
    position: sticky;
    top: 0;
    z-index: 20;
    min-height: var(--mobile-topbar-height);
    align-items: center;
    flex-direction: row;
    padding: 0 var(--space-4);
    background: color-mix(in srgb, var(--color-surface) 94%, transparent);
    backdrop-filter: blur(10px);
  }

  .notifications-page__eyebrow {
    display: none;
  }

  .notifications-page h1 {
    font-size: 20px;
    line-height: 1.2;
    letter-spacing: -0.02em;
  }

  .notifications-page__mark-all {
    border: 0;
    border-radius: 0;
    padding: var(--space-2);
    background: transparent;
    color: var(--color-accent);
    font-size: 13px;
  }

  .notifications-page__mark-all:hover,
  .notifications-page__mark-all:focus-visible {
    border-color: transparent;
    color: var(--color-accent-hover);
  }

  .notifications-page__pending-label {
    padding: var(--space-2) 0;
    font-size: 13px;
  }

  .notifications-page__state {
    min-height: clamp(240px, 45dvh, 380px);
    padding-inline: var(--space-4);
  }

  .notification-card__open {
    grid-template-columns: 40px minmax(0, 1fr) auto;
    gap: 12px;
    padding: 14px 16px;
  }

  .notification-card__avatar {
    width: 40px;
    height: 40px;
  }

  .notification-card__title {
    overflow: visible;
    font-size: 14px;
    overflow-wrap: anywhere;
    text-overflow: clip;
    white-space: normal;
  }
}
</style>
