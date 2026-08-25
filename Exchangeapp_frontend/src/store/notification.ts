import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import {
  getNotifications,
  getUnreadNotificationCount,
  markAllNotificationsRead,
  markNotificationRead as markNotificationReadRequest,
} from '../services/notificationService';
import type { Notification } from '../types/Notification';

export type NotificationViewerCapture = {
  viewerID: number;
  generation: number;
};

const normalizeViewerID = (value: unknown): number | null => {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value <= 0) {
    return null;
  }
  return value;
};

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

export const useNotificationStore = defineStore('notification', () => {
  const viewerID = ref<number | null>(null);
  const viewerGeneration = ref(0);
  const unreadCount = ref(0);
  const unreadLoading = ref(false);
  const unreadError = ref<unknown>(null);

  const items = ref<Notification[]>([]);
  const nextCursor = ref<string | null>(null);
  const loaded = ref(false);
  const loading = ref(false);
  const error = ref<unknown | null>(null);
  const loadingMore = ref(false);
  const loadMoreError = ref<unknown | null>(null);
  const listStale = ref(false);
  const revalidating = ref(false);
  const revalidateError = ref<unknown | null>(null);
  const pendingReadIDs = ref(new Set<number>());
  const markAllPending = ref(false);
  const scrollY = ref(0);
  const listRequestVersion = ref(0);
  const pagingRequestVersion = ref(0);

  let unreadInFlight: { key: string; promise: Promise<number | null> } | null = null;
  let markAllSnapshot: Map<number, boolean> | null = null;
  let markAllPreviousCount = 0;

  const unreadBadge = computed(() => {
    if (unreadCount.value <= 0) {
      return null;
    }
    return unreadCount.value >= 100 ? '99+' : String(unreadCount.value);
  });

  const setUnreadCount = (count: number) => {
    unreadCount.value = Number.isFinite(count) ? Math.max(0, Math.floor(count)) : 0;
  };

  const decrementUnread = (amount = 1) => {
    setUnreadCount(unreadCount.value - Math.max(0, amount));
  };

  const incrementUnread = (amount = 1) => {
    setUnreadCount(unreadCount.value + Math.max(0, amount));
  };

  const captureViewer = (): NotificationViewerCapture | null => {
    if (viewerID.value === null) {
      return null;
    }
    return { viewerID: viewerID.value, generation: viewerGeneration.value };
  };

  const isCurrentViewer = (capture: NotificationViewerCapture | null): boolean => (
    capture !== null
      && capture.viewerID === viewerID.value
      && capture.generation === viewerGeneration.value
  );

  const resetListSession = () => {
    listRequestVersion.value += 1;
    pagingRequestVersion.value += 1;
    items.value = [];
    nextCursor.value = null;
    loaded.value = false;
    loading.value = false;
    error.value = null;
    loadingMore.value = false;
    loadMoreError.value = null;
    listStale.value = false;
    revalidating.value = false;
    revalidateError.value = null;
    pendingReadIDs.value = new Set();
    markAllPending.value = false;
    markAllSnapshot = null;
    scrollY.value = 0;
  };

  const setViewer = (nextViewerID: number | null) => {
    const normalized = normalizeViewerID(nextViewerID);
    if (normalized === viewerID.value) {
      return false;
    }

    viewerID.value = normalized;
    viewerGeneration.value += 1;
    unreadInFlight = null;
    unreadLoading.value = false;
    unreadCount.value = 0;
    unreadError.value = null;
    resetListSession();
    return true;
  };

  const markListStale = () => {
    if (!loaded.value) {
      return;
    }
    listStale.value = true;
    revalidating.value = false;
    revalidateError.value = null;
    listRequestVersion.value += 1;
    pagingRequestVersion.value += 1;
    loadingMore.value = false;
    loadMoreError.value = null;
  };

  const refreshUnreadCount = async (capture: NotificationViewerCapture | null = captureViewer()) => {
    if (!capture || !isCurrentViewer(capture)) {
      return null;
    }
    const key = `${capture.viewerID}:${capture.generation}`;
    if (unreadInFlight?.key === key) {
      return unreadInFlight.promise;
    }

    let request!: Promise<number | null>;
    request = (async () => {
      unreadLoading.value = true;
      unreadError.value = null;
      try {
        const count = await getUnreadNotificationCount();
        if (isCurrentViewer(capture)) {
          const previous = unreadCount.value;
          setUnreadCount(count);
          if (loaded.value && count > previous) {
            markListStale();
          }
        }
        return count;
      } catch (refreshError) {
        if (isCurrentViewer(capture)) {
          unreadError.value = refreshError;
        }
        throw refreshError;
      } finally {
        if (isCurrentViewer(capture)) {
          unreadLoading.value = false;
        }
        if (unreadInFlight?.promise === request) {
          unreadInFlight = null;
        }
      }
    })();
    unreadInFlight = { key, promise: request };
    return request;
  };

  const isCurrentListRequest = (
    capture: NotificationViewerCapture,
    requestVersion: number,
    pagingVersion: number,
  ) => (
    isCurrentViewer(capture)
      && requestVersion === listRequestVersion.value
      && pagingVersion === pagingRequestVersion.value
  );

  const loadInitial = async (force = false) => {
    const capture = captureViewer();
    if (!capture || (loaded.value && !force) || loading.value) {
      return;
    }
    if (force && (pendingReadIDs.value.size > 0 || markAllPending.value)) {
      return;
    }
    if (force) {
      resetListSession();
    }

    const requestVersion = ++listRequestVersion.value;
    const pagingVersion = ++pagingRequestVersion.value;
    loading.value = true;
    error.value = null;
    loadMoreError.value = null;
    try {
      const response = await getNotifications({ limit: 20 });
      if (!isCurrentListRequest(capture, requestVersion, pagingVersion)) {
        return;
      }
      items.value = mergeUniqueNotifications([], response.items);
      nextCursor.value = response.next_cursor;
      loaded.value = true;
      listStale.value = false;
      revalidateError.value = null;
    } catch (loadError) {
      if (isCurrentListRequest(capture, requestVersion, pagingVersion)) {
        error.value = loadError;
      }
    } finally {
      if (isCurrentListRequest(capture, requestVersion, pagingVersion)) {
        loading.value = false;
      }
    }
  };

  const loadMore = async () => {
    const capture = captureViewer();
    if (
      !capture
      || !loaded.value
      || !nextCursor.value
      || listStale.value
      || revalidating.value
      || loadingMore.value
    ) {
      return;
    }

    const requestVersion = ++listRequestVersion.value;
    const pagingVersion = ++pagingRequestVersion.value;
    const cursor = nextCursor.value;
    loadingMore.value = true;
    loadMoreError.value = null;
    try {
      const response = await getNotifications({ limit: 20, cursor });
      if (!isCurrentListRequest(capture, requestVersion, pagingVersion)) {
        return;
      }
      items.value = mergeUniqueNotifications(items.value, response.items);
      nextCursor.value = response.next_cursor;
    } catch (loadError) {
      if (isCurrentListRequest(capture, requestVersion, pagingVersion)) {
        loadMoreError.value = loadError;
      }
    } finally {
      if (isCurrentListRequest(capture, requestVersion, pagingVersion)) {
        loadingMore.value = false;
      }
    }
  };

  const revalidateNotifications = async () => {
    const capture = captureViewer();
    if (
      !capture
      || !loaded.value
      || !listStale.value
      || revalidating.value
      || pendingReadIDs.value.size > 0
      || markAllPending.value
    ) {
      return;
    }

    const requestVersion = ++listRequestVersion.value;
    const pagingVersion = ++pagingRequestVersion.value;
    revalidating.value = true;
    revalidateError.value = null;
    loadingMore.value = false;
    loadMoreError.value = null;
    try {
      const response = await getNotifications({ limit: 20 });
      if (!isCurrentListRequest(capture, requestVersion, pagingVersion)) {
        return;
      }

      const fresh = mergeUniqueNotifications([], response.items);
      const freshIDs = new Set(fresh.map(item => item.id));
      const cachedTail = items.value.filter(item => !freshIDs.has(item.id));
      const readOverrides = new Map(
        items.value
          .filter(item => markAllPending.value || pendingReadIDs.value.has(item.id))
          .map(item => [item.id, item.read]),
      );
      items.value = [...fresh, ...cachedTail].map(item => (
        readOverrides.has(item.id) ? { ...item, read: readOverrides.get(item.id) ?? item.read } : item
      ));
      nextCursor.value = response.next_cursor;
      listStale.value = false;
      revalidateError.value = null;
    } catch (revalidationError) {
      if (isCurrentListRequest(capture, requestVersion, pagingVersion)) {
        listStale.value = true;
        revalidateError.value = revalidationError;
      }
    } finally {
      if (isCurrentListRequest(capture, requestVersion, pagingVersion)) {
        revalidating.value = false;
      }
    }
  };

  const settleRevalidationIfNeeded = () => {
    if (listStale.value && pendingReadIDs.value.size === 0 && !markAllPending.value) {
      void revalidateNotifications();
    }
  };

  const markNotificationRead = async (notificationID: number) => {
    const capture = captureViewer();
    const item = items.value.find(candidate => candidate.id === notificationID);
    if (!capture || !item || item.read || pendingReadIDs.value.has(notificationID)) {
      return;
    }

    const previousUnreadCount = unreadCount.value;
    item.read = true;
    decrementUnread();
    pendingReadIDs.value = new Set(pendingReadIDs.value).add(notificationID);
    try {
      await markNotificationReadRequest(notificationID);
      if (isCurrentViewer(capture)) {
        await refreshUnreadCount(capture).catch(() => undefined);
      }
    } catch (mutationError) {
      if (isCurrentViewer(capture)) {
        const currentItem = items.value.find(candidate => candidate.id === notificationID);
        if (currentItem) {
          currentItem.read = false;
        }
        setUnreadCount(previousUnreadCount);
        await refreshUnreadCount(capture).catch(() => undefined);
      }
      throw mutationError;
    } finally {
      if (isCurrentViewer(capture)) {
        const nextPending = new Set(pendingReadIDs.value);
        nextPending.delete(notificationID);
        pendingReadIDs.value = nextPending;
        settleRevalidationIfNeeded();
      }
    }
  };

  const markAllRead = async () => {
    const capture = captureViewer();
    if (!capture || markAllPending.value || !items.value.some(item => !item.read)) {
      return;
    }

    markAllSnapshot = new Map(items.value.map(item => [item.id, item.read]));
    markAllPreviousCount = unreadCount.value;
    items.value.forEach(item => { item.read = true; });
    setUnreadCount(0);
    markAllPending.value = true;
    try {
      await markAllNotificationsRead();
      if (isCurrentViewer(capture)) {
        await refreshUnreadCount(capture).catch(() => undefined);
      }
    } catch (mutationError) {
      if (isCurrentViewer(capture) && markAllSnapshot) {
        items.value.forEach(item => {
          const previous = markAllSnapshot?.get(item.id);
          if (previous !== undefined) {
            item.read = previous;
          }
        });
        setUnreadCount(markAllPreviousCount);
        await refreshUnreadCount(capture).catch(() => undefined);
      }
      throw mutationError;
    } finally {
      if (isCurrentViewer(capture)) {
        markAllPending.value = false;
        markAllSnapshot = null;
        settleRevalidationIfNeeded();
      }
    }
  };

  const saveScroll = (value: number) => {
    if (Number.isFinite(value) && value >= 0) {
      scrollY.value = value;
    }
  };

  return {
    viewerID,
    viewerGeneration,
    unreadCount,
    unreadBadge,
    unreadLoading,
    unreadError,
    items,
    nextCursor,
    loaded,
    loading,
    error,
    loadingMore,
    loadMoreError,
    listStale,
    revalidating,
    revalidateError,
    pendingReadIDs,
    markAllPending,
    scrollY,
    listRequestVersion,
    pagingRequestVersion,
    setViewer,
    captureViewer,
    isCurrentViewer,
    setUnreadCount,
    decrementUnread,
    incrementUnread,
    refreshUnreadCount,
    loadInitial,
    loadMore,
    revalidateNotifications,
    markNotificationRead,
    markAllRead,
    saveScroll,
  };
});
