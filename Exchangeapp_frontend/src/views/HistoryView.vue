<template>
  <main class="history-view">
    <header class="history-view__header">
      <button class="history-view__back" type="button" aria-label="Back" @click="goBack">
        <AppIcon name="arrow-left" :size="20" />
        <span>History</span>
      </button>
    </header>

    <section
      v-if="currentViewerID === null"
      class="history-view__state history-view__state--auth"
      aria-labelledby="history-auth-title"
    >
      <p id="history-auth-title">Log in to view your history.</p>
      <RouterLink class="history-view__primary" :to="{ name: 'Login' }">Log in</RouterLink>
    </section>

    <template v-else>
      <nav class="history-view__tabs" aria-label="History sections">
        <button class="history-view__tab history-view__tab--active" type="button" aria-selected="true">
          Likes
        </button>
      </nav>

      <section v-if="initialLoading && !loaded" class="history-view__feed history-view__feed--loading" aria-live="polite">
        <article v-for="skeleton in skeletonPosts" :key="skeleton" class="history-skeleton" aria-hidden="true">
          <span class="history-skeleton__author"></span>
          <span class="history-skeleton__title"></span>
          <span class="history-skeleton__line"></span>
          <span class="history-skeleton__line history-skeleton__line--short"></span>
        </article>
      </section>

      <section v-else-if="initialError" class="history-view__state" role="alert" aria-live="polite">
        <p>History could not be loaded.</p>
        <button class="history-view__primary" type="button" @click="retryInitial">Retry</button>
      </section>

      <section v-else-if="showEmpty" class="history-view__state" aria-live="polite">
        <p>No liked posts yet.</p>
      </section>

      <section v-else class="history-view__feed" aria-label="Liked posts">
        <PostCard
          v-for="post in historyPosts"
          :key="post.id"
          :post="post"
          :track-view="false"
          :like-pending="likePendingArticleIDs.has(post.id)"
          @toggle-like="handleLikeToggle"
        />

        <div
          v-if="nextCursor || loadingMore || loadMoreError"
          ref="historySentinelRef"
          class="history-view__sentinel"
          aria-live="polite"
        >
          <span v-if="loadingMore">Loading more posts...</span>
          <template v-else-if="loadMoreError">
            <span>Could not load more posts.</span>
            <button class="history-view__primary" type="button" @click="retryLoadMore">Retry</button>
          </template>
          <button
            v-else-if="!historyIntersectionObserverAvailable && nextCursor"
            class="history-view__primary"
            type="button"
            @click="loadMore"
          >
            Load more posts
          </button>
        </div>

        <p v-if="unlikeError" class="history-view__inline-error" role="status" aria-live="polite">
          {{ unlikeError }}
        </p>
      </section>
    </template>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import PostCard from '../components/feed/PostCard.vue';
import { getLikedHistory } from '../services/historyService';
import { getArticleLikeStates, unlikeArticle } from '../services/likeService';
import { useAuthStore } from '../store/auth';
import type { Article } from '../types/Article';
import type { FeedPost } from '../types/Feed';
import {
  applyFeedLikeStateUpdate,
  articleToFeedPost,
  setFeedPostLikeUnavailable,
} from '../utils/feedPost';

const pageSize = 20;
const router = useRouter();
const authStore = useAuthStore();
const historyIntersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';

const currentViewerID = computed(() => {
  if (!authStore.isAuthenticated) {
    return null;
  }
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});

const viewerIdentityKey = computed(() => (
  currentViewerID.value === null
    ? 'anonymous'
    : `user:${currentViewerID.value}`
));

const historyPosts = ref<FeedPost[]>([]);
const initialLoading = ref(false);
const initialError = ref(false);
const loaded = ref(false);
const nextCursor = ref<string | null>(null);
const loadingMore = ref(false);
const loadMoreError = ref(false);
const unlikeError = ref('');
const historySentinelRef = ref<HTMLElement | null>(null);
const skeletonPosts = [0, 1, 2];
const loadedArticleIDs = new Set<number>();
const suppressedArticleIDs = reactive(new Set<number>());
const likePendingArticleIDs = reactive(new Set<number>());
const likeMutationVersions = new Map<number, number>();

let observer: IntersectionObserver | null = null;
let pageGeneration = 0;
let historyRequestVersion = 0;
let historyPagingVersion = 0;
let likeHydrationGeneration = 0;

const showEmpty = computed(() =>
  loaded.value
  && !initialLoading.value
  && !initialError.value
  && historyPosts.value.length === 0
  && nextCursor.value === null,
);

const disconnectObserver = () => {
  observer?.disconnect();
  observer = null;
};

const resetHistoryState = () => {
  pageGeneration += 1;
  historyRequestVersion += 1;
  historyPagingVersion += 1;
  likeHydrationGeneration += 1;
  disconnectObserver();
  historyPosts.value = [];
  initialLoading.value = false;
  initialError.value = false;
  loaded.value = false;
  nextCursor.value = null;
  loadingMore.value = false;
  loadMoreError.value = false;
  unlikeError.value = '';
  loadedArticleIDs.clear();
  suppressedArticleIDs.clear();
  likePendingArticleIDs.clear();
  likeMutationVersions.clear();
};

const appendHistoryArticles = (articles: Article[]): FeedPost[] => {
  const additions: FeedPost[] = [];
  articles.forEach((article) => {
    if (loadedArticleIDs.has(article.ID)) {
      return;
    }
    loadedArticleIDs.add(article.ID);
    if (suppressedArticleIDs.has(article.ID)) {
      return;
    }
    additions.push(articleToFeedPost(article));
  });

  if (additions.length > 0) {
    historyPosts.value = [...historyPosts.value, ...additions];
  }
  return additions;
};

const findHistoryPost = (articleID: number) =>
  historyPosts.value.find(post => post.id === articleID);

const getLikeMutationVersion = (articleID: number) =>
  likeMutationVersions.get(articleID) ?? 0;

const bumpLikeMutationVersion = (articleID: number) => {
  const nextVersion = getLikeMutationVersion(articleID) + 1;
  likeMutationVersions.set(articleID, nextVersion);
  return nextVersion;
};

const removeHistoryPost = (articleID: number) => {
  historyPosts.value = historyPosts.value.filter(post => post.id !== articleID);
};

const restoreHistoryPost = (post: FeedPost, index: number) => {
  if (suppressedArticleIDs.has(post.id) || findHistoryPost(post.id)) {
    return;
  }
  const nextPosts = [...historyPosts.value];
  nextPosts.splice(Math.min(index, nextPosts.length), 0, post);
  historyPosts.value = nextPosts;
};

const isCurrentHistoryRequest = (
  requestVersion: number,
  generation: number,
  capturedViewerKey: string,
) =>
  requestVersion === historyRequestVersion
  && generation === pageGeneration
  && capturedViewerKey === viewerIdentityKey.value
  && authStore.isAuthenticated
  && currentViewerID.value !== null;

const hydrateHistoryLikeStates = async (
  posts: FeedPost[],
  requestVersion: number,
  generation: number,
  capturedViewerKey: string,
) => {
  const articleIDs = Array.from(new Set(posts.map(post => post.id)));
  if (articleIDs.length === 0) {
    return;
  }

  const hydrationGeneration = likeHydrationGeneration;
  const mutationVersions = new Map(
    articleIDs.map(articleID => [articleID, getLikeMutationVersion(articleID)]),
  );
  const isCurrent = () =>
    isCurrentHistoryRequest(requestVersion, generation, capturedViewerKey)
    && hydrationGeneration === likeHydrationGeneration;

  try {
    const response = await getArticleLikeStates(articleIDs);
    if (!isCurrent()) {
      return;
    }

    const readyArticleIDs = new Set<number>();
    response.items.forEach((item) => {
      const capturedVersion = mutationVersions.get(item.article_id);
      if (
        capturedVersion === undefined
        || getLikeMutationVersion(item.article_id) !== capturedVersion
        || suppressedArticleIDs.has(item.article_id)
        || !findHistoryPost(item.article_id)
      ) {
        return;
      }

      if (!item.liked) {
        suppressedArticleIDs.add(item.article_id);
        removeHistoryPost(item.article_id);
        return;
      }

      readyArticleIDs.add(item.article_id);
      const post = findHistoryPost(item.article_id);
      if (!post) {
        return;
      }
      applyFeedLikeStateUpdate(post, {
        articleId: item.article_id,
        likes: item.likes,
        liked: true,
        status: 'ready',
      });
    });

    response.unavailable_article_ids.forEach((articleID) => {
      if (readyArticleIDs.has(articleID) || suppressedArticleIDs.has(articleID)) {
        return;
      }
      const capturedVersion = mutationVersions.get(articleID);
      const post = findHistoryPost(articleID);
      if (capturedVersion !== undefined && getLikeMutationVersion(articleID) === capturedVersion && post) {
        setFeedPostLikeUnavailable(post);
      }
    });
  } catch {
    if (!isCurrent()) {
      return;
    }
    articleIDs.forEach((articleID) => {
      const capturedVersion = mutationVersions.get(articleID);
      const post = findHistoryPost(articleID);
      if (capturedVersion !== undefined && getLikeMutationVersion(articleID) === capturedVersion && post) {
        setFeedPostLikeUnavailable(post);
      }
    });
  }
};

const loadInitial = async () => {
  const viewerID = currentViewerID.value;
  if (viewerID === null || !authStore.isAuthenticated || initialLoading.value) {
    return;
  }

  const generation = pageGeneration;
  const requestVersion = ++historyRequestVersion;
  const capturedViewerKey = viewerIdentityKey.value;
  historyPagingVersion += 1;
  initialLoading.value = true;
  initialError.value = false;
  loadMoreError.value = false;

  try {
    const response = await getLikedHistory({ limit: pageSize });
    if (!isCurrentHistoryRequest(requestVersion, generation, capturedViewerKey)) {
      return;
    }
    const newPosts = appendHistoryArticles(response.items);
    nextCursor.value = response.next_cursor;
    loaded.value = true;
    void hydrateHistoryLikeStates(newPosts, requestVersion, generation, capturedViewerKey);
  } catch {
    if (isCurrentHistoryRequest(requestVersion, generation, capturedViewerKey)) {
      initialError.value = true;
    }
  } finally {
    if (isCurrentHistoryRequest(requestVersion, generation, capturedViewerKey)) {
      initialLoading.value = false;
      void updateObserver();
    }
  }
};

const loadMore = async () => {
  if (
    currentViewerID.value === null
    || !authStore.isAuthenticated
    || !loaded.value
    || !nextCursor.value
    || initialLoading.value
    || loadingMore.value
    || loadMoreError.value
  ) {
    return;
  }

  const requestedCursor = nextCursor.value;
  const generation = pageGeneration;
  const requestVersion = historyRequestVersion;
  const pagingVersion = ++historyPagingVersion;
  const capturedViewerKey = viewerIdentityKey.value;
  loadingMore.value = true;
  loadMoreError.value = false;

  try {
    const response = await getLikedHistory({ limit: pageSize, cursor: requestedCursor });
    if (
      !isCurrentHistoryRequest(requestVersion, generation, capturedViewerKey)
      || pagingVersion !== historyPagingVersion
      || nextCursor.value !== requestedCursor
    ) {
      return;
    }
    const newPosts = appendHistoryArticles(response.items);
    nextCursor.value = response.next_cursor;
    void hydrateHistoryLikeStates(newPosts, requestVersion, generation, capturedViewerKey);
  } catch {
    if (
      isCurrentHistoryRequest(requestVersion, generation, capturedViewerKey)
      && pagingVersion === historyPagingVersion
    ) {
      loadMoreError.value = true;
    }
  } finally {
    if (
      isCurrentHistoryRequest(requestVersion, generation, capturedViewerKey)
      && pagingVersion === historyPagingVersion
    ) {
      loadingMore.value = false;
      void updateObserver();
    }
  }
};

const retryInitial = () => {
  resetHistoryState();
  void loadInitial();
};

const retryLoadMore = () => {
  if (!nextCursor.value) {
    return;
  }
  loadMoreError.value = false;
  void loadMore();
};

const updateObserver = async () => {
  await nextTick();
  disconnectObserver();
  if (
    !historyIntersectionObserverAvailable
    || !historySentinelRef.value
    || !nextCursor.value
    || loadingMore.value
    || loadMoreError.value
    || currentViewerID.value === null
    || !authStore.isAuthenticated
  ) {
    return;
  }

  observer = new IntersectionObserver((entries) => {
    if (entries.some(entry => entry.isIntersecting)) {
      void loadMore();
    }
  }, { rootMargin: '240px 0px' });
  observer.observe(historySentinelRef.value);
};

const getLikeErrorStatus = (error: unknown) =>
  (error as { response?: { status?: number } }).response?.status;

const handleLikeToggle = async (articleID: number) => {
  const index = historyPosts.value.findIndex(post => post.id === articleID);
  const post = index >= 0 ? historyPosts.value[index] : undefined;
  if (!post || post.likeStatus !== 'ready' || !post.liked || likePendingArticleIDs.has(articleID)) {
    return;
  }

  const previousPost: FeedPost = { ...post };
  const mutationVersion = bumpLikeMutationVersion(articleID);
  const generation = pageGeneration;
  const requestVersion = historyRequestVersion;
  const capturedViewerKey = viewerIdentityKey.value;
  likePendingArticleIDs.add(articleID);
  suppressedArticleIDs.add(articleID);
  removeHistoryPost(articleID);
  unlikeError.value = '';

  const isCurrentMutation = () =>
    isCurrentHistoryRequest(requestVersion, generation, capturedViewerKey)
    && getLikeMutationVersion(articleID) === mutationVersion
    && likePendingArticleIDs.has(articleID);

  try {
    const result = await unlikeArticle(articleID);
    if (!isCurrentMutation()) {
      return;
    }

    if (result.liked === false) {
      suppressedArticleIDs.add(articleID);
    } else {
      suppressedArticleIDs.delete(articleID);
      restoreHistoryPost({
        ...previousPost,
        likeCount: Number.isFinite(result.likes) ? Math.max(0, result.likes) : previousPost.likeCount,
        liked: result.liked,
        likeStatus: 'ready',
      }, index);
    }
    likePendingArticleIDs.delete(articleID);
  } catch (error) {
    if (!isCurrentMutation()) {
      return;
    }
    suppressedArticleIDs.delete(articleID);
    restoreHistoryPost({
      ...previousPost,
      likeStatus: getLikeErrorStatus(error) === 503 ? 'unavailable' : 'ready',
    }, index);
    unlikeError.value = getLikeErrorStatus(error) === 503
      ? 'Likes are temporarily unavailable.'
      : 'Could not remove this like.';
    likePendingArticleIDs.delete(articleID);
  }
};

const goBack = () => {
  const historyState = window.history.state as { back?: string | null } | null;
  if (historyState?.back) {
    router.back();
    return;
  }
  void router.push({ name: 'Home' });
};

watch(
  viewerIdentityKey,
  () => {
    resetHistoryState();
    if (currentViewerID.value !== null) {
      void loadInitial();
    }
  },
  { immediate: true },
);

watch(
  [nextCursor, loadingMore, loadMoreError, () => historyPosts.value.length, currentViewerID],
  () => {
    void updateObserver();
  },
  { flush: 'post' },
);

onBeforeUnmount(resetHistoryState);
</script>

<style scoped>
.history-view {
  min-height: 100vh;
  background: var(--color-surface);
  color: var(--color-text);
}

.history-view__header {
  position: sticky;
  top: 0;
  z-index: 5;
  display: flex;
  align-items: center;
  min-height: 56px;
  padding: 0 var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.history-view__back {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 40px;
  margin: 0;
  border: 0;
  padding: 0;
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  font-size: 22px;
  font-weight: 750;
}

.history-view__back:hover,
.history-view__back:focus-visible {
  color: var(--color-accent);
}

.history-view__tabs {
  display: flex;
  min-height: 48px;
  border-bottom: 1px solid var(--color-border);
}

.history-view__tab {
  position: relative;
  min-width: 92px;
  border: 0;
  padding: 0 var(--space-4);
  background: transparent;
  color: var(--color-text-secondary);
  cursor: default;
  font: inherit;
  font-size: 14px;
  font-weight: 700;
}

.history-view__tab--active {
  color: var(--color-text);
}

.history-view__tab--active::after {
  position: absolute;
  right: var(--space-4);
  bottom: -1px;
  left: var(--space-4);
  height: 2px;
  background: var(--color-accent);
  content: '';
}

.history-view__state {
  display: grid;
  justify-items: center;
  gap: var(--space-3);
  padding: 56px var(--space-5);
  color: var(--color-text-secondary);
  text-align: center;
}

.history-view__state p {
  margin: 0;
}

.history-view__primary {
  display: inline-flex;
  min-height: 40px;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-accent);
  border-radius: var(--radius-pill);
  padding: 0 var(--space-5);
  background: var(--color-accent);
  color: #fff;
  cursor: pointer;
  font: inherit;
  font-size: 14px;
  font-weight: 750;
  text-decoration: none;
}

.history-view__primary:hover,
.history-view__primary:focus-visible {
  border-color: var(--color-accent-hover);
  background: var(--color-accent-hover);
}

.history-view__feed {
  min-width: 0;
}

.history-view__sentinel {
  display: flex;
  min-height: 72px;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  color: var(--color-text-secondary);
  text-align: center;
}

.history-view__inline-error {
  margin: 0;
  padding: 0 var(--space-5) var(--space-4);
  color: var(--color-danger);
  font-size: 13px;
  text-align: center;
}

.history-skeleton {
  display: grid;
  gap: var(--space-2);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
}

.history-skeleton span {
  display: block;
  height: 12px;
  border-radius: var(--radius-sm);
  background: var(--color-surface-subtle);
  animation: history-shimmer 1.2s ease-in-out infinite;
}

.history-skeleton__author {
  width: 36%;
  height: 14px !important;
}

.history-skeleton__title {
  width: 74%;
}

.history-skeleton__line--short {
  width: 52%;
}

@keyframes history-shimmer {
  0%,
  100% { opacity: 0.55; }
  50% { opacity: 1; }
}

@media (prefers-reduced-motion: reduce) {
  .history-skeleton span {
    animation: none;
  }
}

@media (max-width: 799px) {
  .history-view__header {
    top: var(--app-mobile-nav-offset, 0px);
  }
}

@media (max-width: 420px) {
  .history-view__header,
  .history-skeleton {
    padding-inline: var(--space-4);
  }
}
</style>
