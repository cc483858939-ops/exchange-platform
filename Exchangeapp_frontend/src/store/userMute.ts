import { defineStore } from 'pinia';
import { ref, watch } from 'vue';
import { useAuthStore } from './auth';
import { useHomeTimelineStore } from './homeTimeline';
import { useNotificationStore } from './notification';
import {
  getMyMutedUsers,
  muteUser as muteUserRequest,
  unmuteUser as unmuteUserRequest,
  type UserMuteItem,
} from '../services/userMuteService';

export type UserMuteMutationResult = 'updated' | 'failed' | 'ignored';

const normalizeID = (value: unknown): number | null => {
  const id = typeof value === 'number' ? value : Number(value);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
};

export const useUserMuteStore = defineStore('userMute', () => {
  const authStore = useAuthStore();
  const viewerID = ref<number | null>(null);
  const viewerGeneration = ref(0);
  const mutedAuthorIDs = ref(new Set<number>());
  const pendingAuthorIDs = ref(new Set<number>());

  const items = ref<UserMuteItem[]>([]);
  const nextCursor = ref<string | null>(null);
  const listLoaded = ref(false);
  const listLoading = ref(false);
  const listLoadingMore = ref(false);
  const listError = ref(false);
  const listLoadingMoreError = ref(false);
  const listStale = ref(false);
  let listRequestVersion = 0;
  let pagingRequestVersion = 0;

  const resetMutedList = () => {
    listRequestVersion += 1;
    pagingRequestVersion += 1;
    items.value = [];
    nextCursor.value = null;
    listLoaded.value = false;
    listLoading.value = false;
    listLoadingMore.value = false;
    listError.value = false;
    listLoadingMoreError.value = false;
    listStale.value = false;
  };

  const setViewer = (nextViewerID: unknown) => {
    const normalized = authStore.isAuthenticated ? normalizeID(nextViewerID) : null;
    if (normalized === viewerID.value) return false;
    viewerID.value = normalized;
    viewerGeneration.value += 1;
    mutedAuthorIDs.value = new Set();
    pendingAuthorIDs.value = new Set();
    resetMutedList();
    return true;
  };

  const captureViewer = () => (
    authStore.isAuthenticated && viewerID.value !== null
      ? { viewerID: viewerID.value, generation: viewerGeneration.value }
      : null
  );

  const isCurrentViewer = (capture: { viewerID: number; generation: number } | null) => Boolean(
    capture
    && authStore.isAuthenticated
    && capture.viewerID === viewerID.value
    && capture.generation === viewerGeneration.value
    && normalizeID(authStore.currentIdentity?.id) === capture.viewerID
  );

  const isMuted = (authorID: unknown) => {
    const normalized = normalizeID(authorID);
    return normalized !== null && mutedAuthorIDs.value.has(normalized);
  };

  const isPending = (authorID: unknown) => {
    const normalized = normalizeID(authorID);
    return normalized !== null && pendingAuthorIDs.value.has(normalized);
  };

  const syncRelatedSurfaces = (authorID: number, muted: boolean, viewer: number) => {
    const homeTimeline = useHomeTimelineStore();
    homeTimeline.setViewer(viewer);
    homeTimeline.applyMutedAuthorStateLocal(authorID, muted);

    const notifications = useNotificationStore();
    notifications.setViewer(viewer);
    notifications.applyMutedActorStateLocal(authorID, muted);
  };

  const applyKnownState = (
    capture: { viewerID: number; generation: number },
    authorID: number,
    muted: boolean,
  ) => {
    if (!isCurrentViewer(capture)) return false;
    const wasMuted = mutedAuthorIDs.value.has(authorID);
    if (wasMuted === muted) return false;
    const next = new Set(mutedAuthorIDs.value);
    if (muted) next.add(authorID);
    else next.delete(authorID);
    mutedAuthorIDs.value = next;
    syncRelatedSurfaces(authorID, muted, capture.viewerID);
    return true;
  };

  const syncServerMuteState = (forViewerID: number, authorID: number, muted: boolean) => {
    const capture = captureViewer();
    if (!capture || capture.viewerID !== forViewerID || !Number.isSafeInteger(authorID) || authorID <= 0) {
      return false;
    }
    return applyKnownState(capture, authorID, muted);
  };

  const markMuteListStale = (authorID: number, muted: boolean) => {
    listRequestVersion += 1;
    pagingRequestVersion += 1;
    listLoading.value = false;
    listLoadingMore.value = false;
    listLoadingMoreError.value = false;
    if (!muted) {
      items.value = items.value.filter(item => item.user.id !== authorID);
    }
    if (listLoaded.value) listStale.value = true;
  };

  const updateMute = async (authorID: number, muted: boolean): Promise<UserMuteMutationResult> => {
    const capture = captureViewer();
    if (!capture || !Number.isSafeInteger(authorID) || authorID <= 0 || capture.viewerID === authorID) {
      return 'ignored';
    }
    if (pendingAuthorIDs.value.has(authorID)) return 'ignored';
    pendingAuthorIDs.value = new Set(pendingAuthorIDs.value).add(authorID);
    try {
      if (muted) await muteUserRequest(authorID);
      else await unmuteUserRequest(authorID);
      if (!isCurrentViewer(capture)) return 'ignored';
      applyKnownState(capture, authorID, muted);
      markMuteListStale(authorID, muted);
      return 'updated';
    } catch {
      return 'failed';
    } finally {
      if (isCurrentViewer(capture)) {
        const next = new Set(pendingAuthorIDs.value);
        next.delete(authorID);
        pendingAuthorIDs.value = next;
      }
    }
  };

  const muteAuthor = (authorID: number) => updateMute(authorID, true);
  const unmuteAuthor = (authorID: number) => updateMute(authorID, false);

  const isCurrentListRequest = (
    capture: { viewerID: number; generation: number },
    requestVersion: number,
  ) => isCurrentViewer(capture) && requestVersion === listRequestVersion;

  const loadMutedAccounts = async (force = false) => {
    const capture = captureViewer();
    if (!capture || listLoading.value || listLoadingMore.value) return;
    if (listLoaded.value && !listStale.value && !force) return;

    const requestVersion = ++listRequestVersion;
    pagingRequestVersion += 1;
    const pagingVersion = pagingRequestVersion;
    listLoading.value = true;
    listError.value = false;
    listLoadingMoreError.value = false;
    try {
      const response = await getMyMutedUsers({ limit: 20 });
      if (!isCurrentListRequest(capture, requestVersion) || pagingVersion !== pagingRequestVersion) return;
      items.value = response.items;
      mutedAuthorIDs.value = new Set([
        ...mutedAuthorIDs.value,
        ...response.items.map(item => item.user.id),
      ]);
      nextCursor.value = response.next_cursor;
      listLoaded.value = true;
      listStale.value = false;
    } catch {
      if (isCurrentListRequest(capture, requestVersion)) listError.value = true;
    } finally {
      if (isCurrentListRequest(capture, requestVersion) && pagingVersion === pagingRequestVersion) {
        listLoading.value = false;
      }
    }
  };

  const loadMoreMutedAccounts = async () => {
    const capture = captureViewer();
    if (!capture || !listLoaded.value || !nextCursor.value || listStale.value || listLoadingMore.value) return;
    const cursor = nextCursor.value;
    const requestVersion = listRequestVersion;
    const pagingVersion = ++pagingRequestVersion;
    listLoadingMore.value = true;
    listLoadingMoreError.value = false;
    try {
      const response = await getMyMutedUsers({ limit: 20, cursor });
      if (
        !isCurrentListRequest(capture, requestVersion)
        || pagingVersion !== pagingRequestVersion
        || cursor !== nextCursor.value
      ) return;
      const seen = new Set(items.value.map(item => item.user.id));
      items.value = [...items.value, ...response.items.filter(item => !seen.has(item.user.id))];
      mutedAuthorIDs.value = new Set([
        ...mutedAuthorIDs.value,
        ...response.items.map(item => item.user.id),
      ]);
      nextCursor.value = response.next_cursor;
    } catch {
      if (isCurrentListRequest(capture, requestVersion)) listLoadingMoreError.value = true;
    } finally {
      if (isCurrentListRequest(capture, requestVersion) && pagingVersion === pagingRequestVersion) {
        listLoadingMore.value = false;
      }
    }
  };

  watch(
    [() => authStore.currentIdentity?.id, () => authStore.isAuthenticated],
    ([identityID, authenticated]) => setViewer(authenticated ? identityID : null),
    { immediate: true, flush: 'sync' },
  );

  return {
    viewerID,
    viewerGeneration,
    mutedAuthorIDs,
    pendingAuthorIDs,
    items,
    nextCursor,
    listLoaded,
    listLoading,
    listLoadingMore,
    listError,
    listLoadingMoreError,
    listStale,
    setViewer,
    isMuted,
    isPending,
    syncServerMuteState,
    muteAuthor,
    unmuteAuthor,
    loadMutedAccounts,
    loadMoreMutedAccounts,
  };
});
