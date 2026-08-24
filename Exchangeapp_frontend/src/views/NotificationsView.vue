<template>
  <section class="notifications-page" aria-labelledby="notifications-title">
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
          <span class="notification-card__avatar" aria-hidden="true">
            <img v-if="item.actor.avatar_url" :src="item.actor.avatar_url" alt="" />
            <span v-else>{{ actorInitial(item) }}</span>
          </span>
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
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useAuthStore } from '../store/auth';
import { useNotificationStore } from '../store/notification';
import {
  getNotifications,
  markAllNotificationsRead,
  markNotificationRead,
} from '../services/notificationService';
import AppIcon from '../components/icons/AppIcon.vue';
import type { Notification } from '../types/Notification';

const authStore = useAuthStore();
const notificationStore = useNotificationStore();
const router = useRouter();

const items = ref<Notification[]>([]);
const nextCursor = ref<string | null>(null);
const loading = ref(false);
const error = ref<unknown>(null);
const loadingMore = ref(false);
const loadMoreError = ref<unknown>(null);
const pendingReadIDs = ref<Set<number>>(new Set());
const markAllPending = ref(false);
const sentinel = ref<HTMLElement | null>(null);
const observerAvailable = ref(typeof IntersectionObserver !== 'undefined');
let observer: IntersectionObserver | null = null;
let pageGeneration = 0;

const hasUnread = computed(() => items.value.some((item) => !item.read));

const currentViewerID = computed(() => (
  authStore.isAuthenticated ? authStore.currentIdentity?.id ?? null : null
));

const mergeUniqueNotifications = (existing: Notification[], incoming: Notification[]) => {
  const seen = new Set<number>();
  const merged: Notification[] = [];
  for (const item of [...existing, ...incoming]) {
    if (seen.has(item.id)) {
      continue;
    }
    seen.add(item.id);
    merged.push(item);
  }
  return merged;
};

const disconnectObserver = () => {
  observer?.disconnect();
  observer = null;
};

const setupObserver = async () => {
  disconnectObserver();
  if (!observerAvailable.value || !nextCursor.value || !sentinel.value) {
    return;
  }
  await nextTick();
  if (!sentinel.value) {
    return;
  }
  observer = new IntersectionObserver((entries) => {
    if (entries.some((entry) => entry.isIntersecting)) {
      void loadMore();
    }
  }, { rootMargin: '240px 0px' });
  observer.observe(sentinel.value);
};

const loadInitial = async () => {
  const capture = notificationStore.captureViewer();
  const generation = ++pageGeneration;
  disconnectObserver();
  items.value = [];
  nextCursor.value = null;
  error.value = null;
  loadMoreError.value = null;
  pendingReadIDs.value = new Set();
  markAllPending.value = false;
  if (!capture || currentViewerID.value === null) {
    loading.value = false;
    return;
  }
  loading.value = true;
  try {
    const response = await getNotifications({ limit: 20 });
    if (generation !== pageGeneration || !notificationStore.isCurrentViewer(capture)) {
      return;
    }
    items.value = mergeUniqueNotifications([], response.items);
    nextCursor.value = response.next_cursor;
    await setupObserver();
  } catch (loadError) {
    if (generation === pageGeneration && notificationStore.isCurrentViewer(capture)) {
      error.value = loadError;
    }
  } finally {
    if (generation === pageGeneration) {
      loading.value = false;
    }
  }
};

const loadMore = async () => {
  if (!nextCursor.value || loadingMore.value || !notificationStore.captureViewer()) {
    return;
  }
  const capture = notificationStore.captureViewer();
  if (!capture) {
    return;
  }
  const generation = pageGeneration;
  const cursor = nextCursor.value;
  loadingMore.value = true;
  loadMoreError.value = null;
  try {
    const response = await getNotifications({ limit: 20, cursor });
    if (generation !== pageGeneration || !notificationStore.isCurrentViewer(capture)) {
      return;
    }
    items.value = mergeUniqueNotifications(items.value, response.items);
    nextCursor.value = response.next_cursor;
    await setupObserver();
  } catch (loadError) {
    if (generation === pageGeneration && notificationStore.isCurrentViewer(capture)) {
      loadMoreError.value = loadError;
    }
  } finally {
    if (generation === pageGeneration) {
      loadingMore.value = false;
    }
  }
};

const openNotification = (item: Notification) => {
  const capture = notificationStore.captureViewer();
  const index = items.value.findIndex((candidate) => candidate.id === item.id);
  const wasUnread = index >= 0 && !items.value[index].read;
  if (wasUnread && capture) {
    items.value[index].read = true;
    notificationStore.decrementUnread();
    pendingReadIDs.value = new Set(pendingReadIDs.value).add(item.id);
    void markNotificationRead(item.id).then(() => {
      if (notificationStore.isCurrentViewer(capture)) {
        void notificationStore.refreshUnreadCount(capture).catch(() => undefined);
      }
    }).catch(() => {
      if (!notificationStore.isCurrentViewer(capture) || index < 0) {
        return;
      }
      items.value[index].read = false;
      notificationStore.incrementUnread();
      void notificationStore.refreshUnreadCount(capture).catch(() => undefined);
    }).finally(() => {
      const next = new Set(pendingReadIDs.value);
      next.delete(item.id);
      pendingReadIDs.value = next;
    });
  }
  if (item.type === 'user_followed') {
    void router.push({ name: 'UserProfile', params: { id: String(item.actor.id) } });
  } else if (item.article_id !== null) {
    void router.push({ name: 'NewsDetail', params: { id: String(item.article_id) } });
  }
};

const markAll = () => {
  if (markAllPending.value || !hasUnread.value) {
    return;
  }
  const capture = notificationStore.captureViewer();
  if (!capture) {
    return;
  }
  const previousReadState = items.value.map((item) => ({ id: item.id, read: item.read }));
  const previousCount = notificationStore.unreadCount;
  items.value.forEach((item) => { item.read = true; });
  notificationStore.setUnreadCount(0);
  markAllPending.value = true;
  void markAllNotificationsRead().then(() => {
    if (notificationStore.isCurrentViewer(capture)) {
      void notificationStore.refreshUnreadCount(capture).catch(() => undefined);
    }
  }).catch(() => {
    if (!notificationStore.isCurrentViewer(capture)) {
      return;
    }
    const stateByID = new Map(previousReadState.map((state) => [state.id, state.read]));
    items.value.forEach((item) => { item.read = stateByID.get(item.id) ?? item.read; });
    notificationStore.setUnreadCount(previousCount);
    void notificationStore.refreshUnreadCount(capture).catch(() => undefined);
  }).finally(() => {
    if (notificationStore.isCurrentViewer(capture)) {
      markAllPending.value = false;
    }
  });
};

const notificationCopy = (item: Notification) => {
  switch (item.type) {
    case 'post_liked': return 'liked your post.';
    case 'post_replied': return 'replied to your post.';
    case 'user_followed': return 'followed you.';
  }
};

const actorInitial = (item: Notification) => (item.actor.display_name || item.actor.username || '?').slice(0, 1).toUpperCase();

const formatActivityAt = (value: string) => {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return '';
  }
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date);
};

watch(currentViewerID, (nextID, previousID) => {
  if (nextID !== previousID) {
    notificationStore.setViewer(nextID);
    void loadInitial();
  }
}, { immediate: true });

onBeforeUnmount(disconnectObserver);
</script>

<style scoped>
.notifications-page {
  min-height: 100vh;
  min-height: 100dvh;
  padding: clamp(24px, 4vw, 48px) clamp(16px, 4vw, 32px) 72px;
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
  .notifications-page {
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
    min-height: 100vh;
    min-height: 100dvh;
    padding: 0 0 24px;
  }

  .notifications-page__header {
    position: sticky;
    top: var(--mobile-safe-top);
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
