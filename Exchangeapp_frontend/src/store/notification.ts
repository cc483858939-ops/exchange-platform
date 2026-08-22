import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import { getUnreadNotificationCount } from '../services/notificationService';

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

export const useNotificationStore = defineStore('notification', () => {
  const viewerID = ref<number | null>(null);
  const viewerGeneration = ref(0);
  const unreadCount = ref(0);
  const unreadLoading = ref(false);
  const unreadError = ref<unknown>(null);

  const unreadBadge = computed(() => {
    if (unreadCount.value <= 0) {
      return null;
    }
    return unreadCount.value >= 100 ? '99+' : String(unreadCount.value);
  });

  const setViewer = (nextViewerID: number | null) => {
    const normalized = normalizeViewerID(nextViewerID);
    if (normalized === viewerID.value) {
      return false;
    }
    viewerID.value = normalized;
    viewerGeneration.value += 1;
    unreadCount.value = 0;
    unreadError.value = null;
    return true;
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

  const setUnreadCount = (count: number) => {
    unreadCount.value = Number.isFinite(count) ? Math.max(0, Math.floor(count)) : 0;
  };

  const decrementUnread = (amount = 1) => {
    setUnreadCount(unreadCount.value - Math.max(0, amount));
  };

  const incrementUnread = (amount = 1) => {
    setUnreadCount(unreadCount.value + Math.max(0, amount));
  };

  const refreshUnreadCount = async (capture: NotificationViewerCapture | null = captureViewer()) => {
    if (!isCurrentViewer(capture)) {
      return null;
    }
    unreadLoading.value = true;
    unreadError.value = null;
    try {
      const count = await getUnreadNotificationCount();
      if (isCurrentViewer(capture)) {
        setUnreadCount(count);
      }
      return count;
    } catch (error) {
      if (isCurrentViewer(capture)) {
        unreadError.value = error;
      }
      throw error;
    } finally {
      if (isCurrentViewer(capture)) {
        unreadLoading.value = false;
      }
    }
  };

  return {
    viewerID,
    viewerGeneration,
    unreadCount,
    unreadBadge,
    unreadLoading,
    unreadError,
    setViewer,
    captureViewer,
    isCurrentViewer,
    setUnreadCount,
    decrementUnread,
    incrementUnread,
    refreshUnreadCount,
  };
});
