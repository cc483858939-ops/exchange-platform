import { ref } from 'vue';
import { defineStore } from 'pinia';
import {
  followUser,
  searchUsers,
  unfollowUser,
  type UserConnectionItem,
  type UserConnectionPage,
} from '../services/userService';
import { syncExternalFollowState } from './sessionSync';

const pageSize = 20;

export const normalizeSearchQuery = (value: string) => {
  let normalized = value.trim();
  if (normalized.startsWith('@')) {
    normalized = normalized.slice(1).trim();
  }
  return normalized;
};

const normalizeViewerID = (value: unknown): number | null => (
  typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : null
);

export const useSearchSessionStore = defineStore('searchSession', () => {
  const viewerID = ref<number | null>(null);
  const viewerGeneration = ref(0);
  const query = ref('');
  const inputValue = ref('');
  const items = ref<UserConnectionItem[]>([]);
  const loaded = ref(false);
  const initialLoading = ref(false);
  const initialError = ref('');
  const nextOffset = ref(0);
  const hasMore = ref(false);
  const loadingMore = ref(false);
  const loadMoreError = ref('');
  const scrollY = ref(0);
  const pageGeneration = ref(0);
  const paginationRequestVersion = ref(0);
  const pendingMutationIDs = ref(new Set<number>());
  const mutationErrors = ref(new Map<number, string>());
  const mutationVersions = ref(new Map<number, number>());
  const mutationSequence = ref(0);
  const loadedUserIDs = new Set<number>();

  const clearPageState = () => {
    pageGeneration.value += 1;
    paginationRequestVersion.value += 1;
    items.value = [];
    loaded.value = false;
    initialLoading.value = false;
    initialError.value = '';
    nextOffset.value = 0;
    hasMore.value = false;
    loadingMore.value = false;
    loadMoreError.value = '';
    scrollY.value = 0;
    loadedUserIDs.clear();
  };

  const setViewer = (nextViewerID: number | null) => {
    const normalized = normalizeViewerID(nextViewerID);
    if (normalized === viewerID.value) {
      return false;
    }

    viewerID.value = normalized;
    viewerGeneration.value += 1;
    clearPageState();
    inputValue.value = query.value;
    pendingMutationIDs.value = new Set();
    mutationErrors.value = new Map();
    mutationVersions.value.clear();
    mutationSequence.value += 1;
    return true;
  };

  const appendPage = (page: UserConnectionPage) => {
    nextOffset.value += page.items.length;
    const additions = page.items.filter((item) => {
      if (loadedUserIDs.has(item.user.id)) {
        return false;
      }
      loadedUserIDs.add(item.user.id);
      return true;
    });
    items.value = [...items.value, ...additions];
    hasMore.value = page.has_more;
    loaded.value = true;
  };

  const currentPageRequest = (
    capturedPageGeneration: number,
    capturedRequestVersion: number,
    capturedQuery: string,
    capturedViewerID: number,
    capturedViewerGeneration: number,
  ) => (
    capturedPageGeneration === pageGeneration.value
      && capturedRequestVersion === paginationRequestVersion.value
      && capturedQuery === query.value
      && capturedViewerID === viewerID.value
      && capturedViewerGeneration === viewerGeneration.value
  );

  const loadInitial = async (force = false) => {
    const capturedViewerID = viewerID.value;
    const capturedQuery = query.value;
    if (capturedViewerID === null || !capturedQuery) {
      return;
    }
    if (initialLoading.value || (loaded.value && !force)) {
      return;
    }

    const capturedPageGeneration = pageGeneration.value;
    const capturedRequestVersion = ++paginationRequestVersion.value;
    const capturedViewerGeneration = viewerGeneration.value;
    initialLoading.value = true;
    initialError.value = '';
    try {
      const page = await searchUsers({ q: capturedQuery, limit: pageSize, offset: 0 });
      if (!currentPageRequest(
        capturedPageGeneration,
        capturedRequestVersion,
        capturedQuery,
        capturedViewerID,
        capturedViewerGeneration,
      )) {
        return;
      }
      appendPage(page);
    } catch {
      if (currentPageRequest(
        capturedPageGeneration,
        capturedRequestVersion,
        capturedQuery,
        capturedViewerID,
        capturedViewerGeneration,
      )) {
        initialError.value = 'Could not search people.';
      }
    } finally {
      if (currentPageRequest(
        capturedPageGeneration,
        capturedRequestVersion,
        capturedQuery,
        capturedViewerID,
        capturedViewerGeneration,
      )) {
        initialLoading.value = false;
      }
    }
  };

  const activateQuery = (nextQuery: string) => {
    const normalized = normalizeSearchQuery(nextQuery);
    inputValue.value = normalized;
    if (normalized === query.value) {
      if (normalized && !loaded.value && !initialLoading.value) {
        void loadInitial();
      }
      return false;
    }

    query.value = normalized;
    clearPageState();
    if (normalized && viewerID.value !== null) {
      void loadInitial();
    }
    return true;
  };

  const reload = () => {
    clearPageState();
    if (query.value && viewerID.value !== null) {
      void loadInitial(true);
    }
  };

  const loadMore = async () => {
    const capturedViewerID = viewerID.value;
    const capturedQuery = query.value;
    if (
      capturedViewerID === null
      || !capturedQuery
      || !loaded.value
      || !hasMore.value
      || loadingMore.value
      || initialLoading.value
    ) {
      return;
    }

    const capturedPageGeneration = pageGeneration.value;
    const capturedRequestVersion = ++paginationRequestVersion.value;
    const capturedViewerGeneration = viewerGeneration.value;
    const capturedOffset = nextOffset.value;
    loadingMore.value = true;
    loadMoreError.value = '';
    try {
      const page = await searchUsers({ q: capturedQuery, limit: pageSize, offset: capturedOffset });
      if (
        !currentPageRequest(
          capturedPageGeneration,
          capturedRequestVersion,
          capturedQuery,
          capturedViewerID,
          capturedViewerGeneration,
        )
        || nextOffset.value !== capturedOffset
      ) {
        return;
      }
      appendPage(page);
    } catch {
      if (currentPageRequest(
        capturedPageGeneration,
        capturedRequestVersion,
        capturedQuery,
        capturedViewerID,
        capturedViewerGeneration,
      )) {
        loadMoreError.value = 'Could not load more users.';
      }
    } finally {
      if (currentPageRequest(
        capturedPageGeneration,
        capturedRequestVersion,
        capturedQuery,
        capturedViewerID,
        capturedViewerGeneration,
      )) {
        loadingMore.value = false;
      }
    }
  };

  const toggleFollow = async (userID: number) => {
    const index = items.value.findIndex(item => item.user.id === userID);
    const capturedViewerID = viewerID.value;
    if (
      index < 0
      || capturedViewerID === null
      || userID === capturedViewerID
      || pendingMutationIDs.value.has(userID)
    ) {
      return;
    }

    const previous = items.value[index].following;
    const capturedViewerGeneration = viewerGeneration.value;
    const version = mutationSequence.value + 1;
    mutationSequence.value = version;
    mutationVersions.value.set(userID, version);
    pendingMutationIDs.value = new Set(pendingMutationIDs.value).add(userID);
    const nextErrors = new Map(mutationErrors.value);
    nextErrors.delete(userID);
    mutationErrors.value = nextErrors;
    items.value = items.value.map(item => (
      item.user.id === userID ? { ...item, following: !previous } : item
    ));

    const mutationCurrent = () => (
      capturedViewerID === viewerID.value
        && capturedViewerGeneration === viewerGeneration.value
        && mutationVersions.value.get(userID) === version
        && pendingMutationIDs.value.has(userID)
    );

    try {
      const response = previous ? await unfollowUser(userID) : await followUser(userID);
      if (!mutationCurrent() || response.user_id !== userID) {
        return;
      }
      const currentIndex = items.value.findIndex(item => item.user.id === userID);
      if (currentIndex >= 0) {
        items.value = items.value.map(item => (
          item.user.id === userID ? { ...item, following: response.following } : item
        ));
      }
      const nextPending = new Set(pendingMutationIDs.value);
      nextPending.delete(userID);
      pendingMutationIDs.value = nextPending;
      const settledErrors = new Map(mutationErrors.value);
      settledErrors.delete(userID);
      mutationErrors.value = settledErrors;
      syncExternalFollowState(response);
    } catch {
      if (!mutationCurrent()) {
        return;
      }
      items.value = items.value.map(item => (
        item.user.id === userID ? { ...item, following: previous } : item
      ));
      const nextPending = new Set(pendingMutationIDs.value);
      nextPending.delete(userID);
      pendingMutationIDs.value = nextPending;
      mutationErrors.value = new Map(mutationErrors.value).set(userID, 'Could not update follow status.');
    }
  };

  const saveScroll = (value: number) => {
    if (Number.isFinite(value) && value >= 0) {
      scrollY.value = value;
    }
  };

  return {
    viewerID,
    query,
    inputValue,
    items,
    loaded,
    initialLoading,
    initialError,
    nextOffset,
    hasMore,
    loadingMore,
    loadMoreError,
    scrollY,
    pageGeneration,
    paginationRequestVersion,
    pendingMutationIDs,
    mutationErrors,
    mutationVersions,
    mutationSequence,
    setViewer,
    activateQuery,
    loadInitial,
    reload,
    loadMore,
    toggleFollow,
    saveScroll,
  };
});
