<template>
  <main class="home-view">
    <header class="home-feed-header">
      <h1>Home</h1>
      <FeedTabs :active-tab="activeTab" @select="selectTab" />
    </header>

    <div
      :id="'feed-panel-' + activeTab"
      class="home-feed-panel"
      role="tabpanel"
      tabindex="0"
      :aria-labelledby="'feed-tab-' + activeTab"
    >
    <section
      v-if="!authStore.isAuthenticated"
      class="home-state home-state--auth"
      aria-labelledby="home-auth-title"
    >
      <h2 id="home-auth-title">Sign in to view your financial feed</h2>
      <div class="home-state__actions">
        <RouterLink class="home-state__primary" :to="{ name: 'Login' }">Log in</RouterLink>
        <RouterLink class="home-state__secondary" :to="{ name: 'Register' }">Sign up</RouterLink>
      </div>
    </section>

    <section
      v-else-if="activeFeedStatus.loading && !hasRecentlyPublishedPosts"
      class="feed-list feed-list--loading"
      :aria-labelledby="'feed-tab-' + activeTab"
    >
      <article v-for="skeleton in skeletonPosts" :key="skeleton" class="feed-skeleton" aria-hidden="true">
        <span class="feed-skeleton__author"></span>
        <span class="feed-skeleton__title"></span>
        <span class="feed-skeleton__line"></span>
        <span class="feed-skeleton__line feed-skeleton__line--short"></span>
      </article>
    </section>

    <section
      v-else-if="activeFeedStatus.error && !hasRecentlyPublishedPosts"
      class="home-state"
      aria-live="polite"
    >
      <h2>Feed unavailable</h2>
      <button class="home-state__primary" type="button" @click="retryActiveFeed">Retry</button>
    </section>

    <section
      v-else-if="activeFeedStatus.empty && !hasRecentlyPublishedPosts"
      class="home-state"
      aria-live="polite"
    >
      <h2>{{ activeTab === 'for-you' ? 'No recommendations yet' : 'No posts from people you follow yet' }}</h2>
    </section>

    <section
      v-else
      class="feed-list"
    >
      <template v-if="activeTab === 'for-you'">
        <PostCard
          v-for="post in feedStore.recentlyPublishedPosts"
          :key="'recent-' + post.id"
          :post="post"
          :like-pending="likePendingArticleIds.has(post.id)"
          :show-delete="canDeletePost(post)"
          :delete-pending="pendingDeleteArticleIds.has(post.id)"
          :delete-error="deleteErrors.get(post.id) || ''"
          @toggle-like="handleLikeToggle"
          @delete-post="handleDeletePost"
        />

        <div
          v-if="forYouFeed.loading && hasRecentlyPublishedPosts"
          class="home-feed-inline-state"
          aria-live="polite"
        >
          Loading recommendations...
        </div>
        <div
          v-else-if="forYouFeed.error && hasRecentlyPublishedPosts"
          class="home-feed-inline-state"
          aria-live="polite"
        >
          <span>Could not load recommendations.</span>
          <button class="home-state__primary" type="button" @click="loadForYou(true)">
            Retry
          </button>
        </div>

        <div
          v-for="item in visibleForYouItems"
          :key="item.article.id"
          class="recommendation-card-wrapper"
          :ref="element => bindRecommendationCard(element, item)"
        >
          <PostCard
            :post="item.post"
            :like-pending="likePendingArticleIds.has(item.post.id)"
            :show-not-interested="true"
            :show-delete="canDeletePost(item.post)"
            :delete-pending="pendingDeleteArticleIds.has(item.post.id)"
            :delete-error="deleteErrors.get(item.post.id) || ''"
            @article-click="handleRecommendationClick(item.article)"
            @toggle-like="handleLikeToggle"
            @not-interested="handleNotInterested"
            @delete-post="handleDeletePost"
          />
        </div>
      </template>

      <template v-else>
        <PostCard
          v-for="post in followingFeed.items"
          :key="post.id"
          :post="post"
          :like-pending="likePendingArticleIds.has(post.id)"
          :show-delete="canDeletePost(post)"
          :delete-pending="pendingDeleteArticleIds.has(post.id)"
          :delete-error="deleteErrors.get(post.id) || ''"
          @toggle-like="handleLikeToggle"
          @delete-post="handleDeletePost"
        />

        <div
          v-if="followingFeed.nextCursor || followingFeed.loadingMore || followingFeed.loadMoreError"
          ref="followingSentinelRef"
          class="home-feed-sentinel"
          aria-live="polite"
        >
          <span v-if="followingFeed.loadingMore">Loading more posts...</span>
          <template v-else-if="followingFeed.loadMoreError">
            <span>Could not load more posts.</span>
            <button class="home-state__primary" type="button" @click="retryFollowingLoadMore">
              Retry
            </button>
          </template>
          <button
            v-else-if="!followingIntersectionObserverAvailable && followingFeed.nextCursor"
            class="home-state__primary"
            type="button"
            @click="loadMoreFollowing"
          >
            Load more posts
          </button>
        </div>
      </template>
    </section>

    </div>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue';
import type { ComponentPublicInstance } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import FeedTabs from '../components/feed/FeedTabs.vue';
import PostCard from '../components/feed/PostCard.vue';
import { deleteArticle, getFollowingTimeline } from '../services/articleService';
import { getArticleRecommendations } from '../services/recommendationService';
import { getArticleLikeStates, likeArticle, unlikeArticle } from '../services/likeService';
import { savePendingRecommendationAttribution } from '../services/recommendationAttribution';
import { getRecommendationTelemetry } from '../services/recommendationTelemetry';
import { useAuthStore } from '../store/auth';
import { useFeedStore } from '../store/feed';
import type { Article } from '../types/Article';
import type { RecommendedArticle } from '../types/Recommendation';
import type { FeedLikeStateUpdate, FeedPost, FeedTab } from '../types/Feed';
import {
  applyFeedLikeStateUpdate,
  articleToFeedPost,
  recommendationToFeedPost,
  setFeedPostLikeUnavailable,
} from '../utils/feedPost';

type FeedState<T> = {
  items: T[];
  loading: boolean;
  error: boolean;
  loaded: boolean;
};

type RecommendationFeedItem = {
  article: RecommendedArticle;
  post: FeedPost;
};

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const feedStore = useFeedStore();
const recommendationTelemetry = getRecommendationTelemetry(() => authStore.token);

type FollowingFeedState = FeedState<FeedPost> & {
  nextCursor: string | null;
  loadingMore: boolean;
  loadMoreError: boolean;
};

const followingFeed = reactive<FollowingFeedState>({
  items: [],
  loading: false,
  error: false,
  loaded: false,
  nextCursor: null,
  loadingMore: false,
  loadMoreError: false,
});

const forYouFeed = reactive<FeedState<RecommendationFeedItem>>({
  items: [],
  loading: false,
  error: false,
  loaded: false,
});

const skeletonPosts = [0, 1, 2];
const recommendationCardElements = new Map<number, HTMLElement>();
const followingLoadedArticleIds = new Set<number>();
const followingSentinelRef = ref<HTMLElement | null>(null);
const followingIntersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
let followingObserver: IntersectionObserver | null = null;
let authGeneration = 0;
let followingRequestVersion = 0;
let followingPagingVersion = 0;
let forYouRequestVersion = 0;
const likePendingArticleIds = reactive(new Set<number>());
const likeMutationVersions = new Map<number, number>();
const pendingDeleteArticleIds = reactive(new Set<number>());
const deleteErrors = reactive(new Map<number, string>());
let homeLikeGeneration = 0;

const activeTab = computed<FeedTab>(() => route.query.tab === 'following' ? 'following' : 'for-you');
const activeFeedStatus = computed(() => {
  const state = activeTab.value === 'for-you' ? forYouFeed : followingFeed;
  return {
    loading: state.loading || (!state.loaded && !state.error),
    error: state.error,
    empty: state.loaded && !state.loading && !state.error && state.items.length === 0,
  };
});

const hasRecentlyPublishedPosts = computed(
  () => activeTab.value === 'for-you' && feedStore.recentlyPublishedPosts.length > 0,
);

const recentlyPublishedIDs = computed(
  () => new Set(feedStore.recentlyPublishedPosts.map((post) => post.id)),
);

const visibleForYouItems = computed(() =>
  forYouFeed.items.filter((item) =>
    !recentlyPublishedIDs.value.has(item.post.id)
    && !feedStore.isArticleDeleted(item.post.id)
  ),
);

const currentViewerID = () => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isFinite(id) && id > 0 ? id : null;
};

const canDeletePost = (post: FeedPost) =>
  authStore.isAuthenticated
  && currentViewerID() !== null
  && post.author.id === currentViewerID();

const selectTab = (tab: FeedTab) => {
  if (activeTab.value === tab) {
    return;
  }
  void router.push({
    name: 'Home',
    query: {
      ...route.query,
      tab,
    },
  });
};

const normalizeRouteTab = (value: unknown) => {
  if (value === undefined || value === 'for-you' || value === 'following') {
    return;
  }
  void router.replace({
    name: 'Home',
    query: {
      ...route.query,
      tab: 'for-you',
    },
  });
};

const disconnectFollowingObserver = () => {
  followingObserver?.disconnect();
  followingObserver = null;
};

const resetFollowingFeed = () => {
  followingRequestVersion += 1;
  followingPagingVersion += 1;
  followingFeed.items = [];
  followingFeed.loading = false;
  followingFeed.error = false;
  followingFeed.loaded = false;
  followingFeed.nextCursor = null;
  followingFeed.loadingMore = false;
  followingFeed.loadMoreError = false;
  followingLoadedArticleIds.clear();
  disconnectFollowingObserver();
};

const resetForYou = () => {
  forYouFeed.items = [];
  forYouFeed.loading = false;
  forYouFeed.error = false;
  forYouFeed.loaded = false;
};

const invalidateAfterLogout = () => {
  authGeneration += 1;
  invalidateHomeLikeWork();
  pendingDeleteArticleIds.clear();
  deleteErrors.clear();
  forYouRequestVersion += 1;
  resetFollowingFeed();
  resetForYou();
  recommendationCardElements.clear();
  recommendationTelemetry.clearSession();
};

const isCurrentFollowingRequest = (version: number, generation: number) =>
  version === followingRequestVersion
  && generation === authGeneration
  && authStore.isAuthenticated;

const isCurrentFollowingPageLineage = (
  requestVersion: number,
  generation: number,
  pagingVersion: number,
) =>
  requestVersion === followingRequestVersion
  && generation === authGeneration
  && pagingVersion === followingPagingVersion
  && authStore.isAuthenticated;

const isCurrentForYouRequest = (version: number, generation: number) =>
  version === forYouRequestVersion
  && generation === authGeneration
  && authStore.isAuthenticated;

const getLikeMutationVersion = (articleId: number) =>
  likeMutationVersions.get(articleId) ?? 0;

const bumpLikeMutationVersion = (articleId: number) => {
  const nextVersion = getLikeMutationVersion(articleId) + 1;
  likeMutationVersions.set(articleId, nextVersion);
  return nextVersion;
};

const invalidateHomeLikeWork = () => {
  homeLikeGeneration += 1;
  likePendingArticleIds.clear();
  likeMutationVersions.clear();
};

const findHomePost = (articleId: number): FeedPost | undefined => {
  if (feedStore.isArticleDeleted(articleId)) {
    return undefined;
  }
  const recentlyPublishedPost = feedStore.recentlyPublishedPosts.find((post) => post.id === articleId);
  const followingPost = followingFeed.items.find((post) => post.id === articleId);
  const forYouPost = forYouFeed.items.find((item) => item.post.id === articleId)?.post;
  return activeTab.value === 'for-you'
    ? recentlyPublishedPost || forYouPost || followingPost
    : followingPost || forYouPost || recentlyPublishedPost;
};

const forEachHomePost = (articleId: number, callback: (post: FeedPost) => void) => {
  if (feedStore.isArticleDeleted(articleId)) {
    return;
  }
  feedStore.recentlyPublishedPosts.forEach((post) => {
    if (post.id === articleId) {
      callback(post);
    }
  });
  followingFeed.items.forEach((post) => {
    if (post.id === articleId) {
      callback(post);
    }
  });
  forYouFeed.items.forEach((item) => {
    if (item.post.id === articleId) {
      callback(item.post);
    }
  });
};

const applyHomeLikeUpdate = (update: FeedLikeStateUpdate, expectedVersion?: number) => {
  if (
    expectedVersion !== undefined
    && getLikeMutationVersion(update.articleId) !== expectedVersion
  ) {
    return false;
  }

  let applied = false;
  forEachHomePost(update.articleId, (post) => {
    applied = applyFeedLikeStateUpdate(post, update) || applied;
  });
  return applied;
};

const markHomeHydrationUnavailable = (articleIds: number[], versions: Map<number, number>) => {
  articleIds.forEach((articleId) => {
    const capturedVersion = versions.get(articleId);
    if (capturedVersion === undefined || getLikeMutationVersion(articleId) !== capturedVersion) {
      return;
    }
    forEachHomePost(articleId, (post) => {
      if (post.likeStatus === 'unknown') {
        setFeedPostLikeUnavailable(post);
      }
    });
  });
};

const hydrateHomeLikeStates = async (
  articleIds: number[],
  isCurrent: () => boolean,
) => {
  const uniqueIds = Array.from(new Set(articleIds));
  if (uniqueIds.length === 0) {
    return;
  }

  const versions = new Map(
    uniqueIds.map((articleId) => [articleId, getLikeMutationVersion(articleId)]),
  );

  try {
    const response = await getArticleLikeStates(uniqueIds);
    if (!isCurrent()) {
      return;
    }

    const readyIds = new Set<number>();
    response.items.forEach((item) => {
      const capturedVersion = versions.get(item.article_id);
      if (
        capturedVersion === undefined
        || getLikeMutationVersion(item.article_id) !== capturedVersion
        || !findHomePost(item.article_id)
      ) {
        return;
      }

      readyIds.add(item.article_id);
      applyHomeLikeUpdate({
        articleId: item.article_id,
        likes: item.likes,
        liked: item.liked,
        status: 'ready',
      }, capturedVersion);
    });

    response.unavailable_article_ids.forEach((articleId) => {
      if (readyIds.has(articleId)) {
        return;
      }
      const capturedVersion = versions.get(articleId);
      if (
        capturedVersion !== undefined
        && getLikeMutationVersion(articleId) === capturedVersion
        && findHomePost(articleId)
      ) {
        applyHomeLikeUpdate({
          articleId,
          likes: 0,
          liked: false,
          status: 'unavailable',
        }, capturedVersion);
      }
    });
  } catch {
    if (isCurrent()) {
      markHomeHydrationUnavailable(uniqueIds, versions);
    }
  }
};

const getLikeErrorStatus = (error: unknown) =>
  (error as { response?: { status?: number } }).response?.status;

const appendFollowingArticles = (rawArticles: Article[]): FeedPost[] => {
  const newPosts = rawArticles
    .filter((article) => {
      if (followingLoadedArticleIds.has(article.ID)) {
        return false;
      }
      followingLoadedArticleIds.add(article.ID);
      return !feedStore.isArticleDeleted(article.ID);
    })
    .map(articleToFeedPost);

  if (newPosts.length > 0) {
    followingFeed.items = [...followingFeed.items, ...newPosts];
  }
  return newPosts;
};

const loadFollowing = async (force = false) => {
  if (!authStore.isAuthenticated || followingFeed.loading && !force) {
    return;
  }
  if (followingFeed.loaded && !force) {
    void updateFollowingObserver();
    return;
  }
  if (force) {
    resetFollowingFeed();
  }

  const version = ++followingRequestVersion;
  const generation = authGeneration;
  const pagingVersion = ++followingPagingVersion;
  followingFeed.loading = true;
  followingFeed.error = false;
  followingFeed.loadMoreError = false;

  try {
    const response = await getFollowingTimeline({ limit: 20 });
    if (!isCurrentFollowingRequest(version, generation)) {
      return;
    }
    const newPosts = appendFollowingArticles(response.items);
    followingFeed.nextCursor = response.next_cursor;
    followingFeed.loaded = true;
    const likeGeneration = homeLikeGeneration;
    void hydrateHomeLikeStates(
      newPosts.map((post) => post.id),
      () => isCurrentFollowingRequest(version, generation)
        && homeLikeGeneration === likeGeneration,
    );
  } catch {
    if (!isCurrentFollowingRequest(version, generation)) {
      return;
    }
    followingFeed.error = true;
  } finally {
    if (version === followingRequestVersion && generation === authGeneration && pagingVersion === followingPagingVersion) {
      followingFeed.loading = false;
    }
  }
};

const loadMoreFollowing = async () => {
  if (
    !authStore.isAuthenticated
    || activeTab.value !== 'following'
    || !followingFeed.loaded
    || !followingFeed.nextCursor
    || followingFeed.loading
    || followingFeed.loadingMore
    || followingFeed.loadMoreError
  ) {
    return;
  }

  const requestedCursor = followingFeed.nextCursor;
  const requestVersion = followingRequestVersion;
  const generation = authGeneration;
  const pagingVersion = ++followingPagingVersion;
  followingFeed.loadingMore = true;
  followingFeed.loadMoreError = false;

  try {
    const response = await getFollowingTimeline({ limit: 20, cursor: requestedCursor });
    if (
      !isCurrentFollowingPageLineage(requestVersion, generation, pagingVersion)
      || followingFeed.nextCursor !== requestedCursor
    ) {
      return;
    }

    const newPosts = appendFollowingArticles(response.items);
    followingFeed.nextCursor = response.next_cursor;
    const likeGeneration = homeLikeGeneration;
    void hydrateHomeLikeStates(
      newPosts.map((post) => post.id),
      () => isCurrentFollowingRequest(requestVersion, generation)
        && homeLikeGeneration === likeGeneration,
    );
  } catch {
    if (isCurrentFollowingPageLineage(requestVersion, generation, pagingVersion)) {
      followingFeed.loadMoreError = true;
    }
  } finally {
    if (isCurrentFollowingPageLineage(requestVersion, generation, pagingVersion)) {
      followingFeed.loadingMore = false;
    }
  }
};

const retryFollowingLoadMore = () => {
  if (!followingFeed.nextCursor) {
    return;
  }
  followingFeed.loadMoreError = false;
  void loadMoreFollowing();
};

const updateFollowingObserver = () => {
  disconnectFollowingObserver();

  if (
    !followingIntersectionObserverAvailable
    || activeTab.value !== 'following'
    || !followingSentinelRef.value
    || !followingFeed.nextCursor
    || followingFeed.loadingMore
    || followingFeed.loadMoreError
    || !authStore.isAuthenticated
  ) {
    return;
  }

  followingObserver = new IntersectionObserver((entries) => {
    if (entries.some((entry) => entry.isIntersecting)) {
      void loadMoreFollowing();
    }
  }, { rootMargin: '240px 0px' });
  followingObserver.observe(followingSentinelRef.value);
};

const handleLikeToggle = async (articleId: number) => {
  const post = findHomePost(articleId);
  if (
    !post
    || post.likeStatus !== 'ready'
    || likePendingArticleIds.has(articleId)
  ) {
    return;
  }

  const previousLiked = post.liked;
  const previousLikes = post.likeCount;
  const mutationVersion = bumpLikeMutationVersion(articleId);
  const likeGeneration = homeLikeGeneration;
  const generation = authGeneration;
  likePendingArticleIds.add(articleId);

  applyHomeLikeUpdate({
    articleId,
    likes: previousLiked ? Math.max(0, previousLikes - 1) : previousLikes + 1,
    liked: !previousLiked,
    status: 'ready',
  }, mutationVersion);

  const isCurrentMutation = () =>
    authStore.isAuthenticated
    && authGeneration === generation
    && homeLikeGeneration === likeGeneration
    && getLikeMutationVersion(articleId) === mutationVersion
    && likePendingArticleIds.has(articleId);

  try {
    const result = previousLiked
      ? await unlikeArticle(articleId)
      : await likeArticle(articleId);

    if (isCurrentMutation()) {
      const settledVersion = bumpLikeMutationVersion(articleId);
      applyHomeLikeUpdate({
        articleId,
        likes: result.likes,
        liked: result.liked,
        status: 'ready',
      }, settledVersion);
      likePendingArticleIds.delete(articleId);
    }
  } catch (error) {
    if (!isCurrentMutation()) {
      return;
    }

    const settledVersion = bumpLikeMutationVersion(articleId);
    applyHomeLikeUpdate({
      articleId,
      likes: previousLikes,
      liked: previousLiked,
      status: 'ready',
    }, settledVersion);

    if (getLikeErrorStatus(error) === 503) {
      applyHomeLikeUpdate({
        articleId,
        likes: previousLikes,
        liked: previousLiked,
        status: 'unavailable',
      }, settledVersion);
    }
    likePendingArticleIds.delete(articleId);
  }
};
const bindCurrentRecommendationCards = async () => {
  await nextTick();
  if (!authStore.isAuthenticated || activeTab.value !== 'for-you') {
    return;
  }
  visibleForYouItems.value.forEach((item) => {
    const element = recommendationCardElements.get(item.article.id);
    if (element) {
      recommendationTelemetry.observeFeedCard(element, item.article.id, item.article.tracking);
    }
  });
};

const resetRecommendationObservation = () => {
  recommendationTelemetry.resetObservedCards();
  void recommendationTelemetry.flush(false);
  recommendationCardElements.clear();
};

const loadForYou = async (force = false) => {
  if (!authStore.isAuthenticated || forYouFeed.loading && !force) {
    return;
  }
  if (force) {
    invalidateHomeLikeWork();
  }
  if (forYouFeed.loaded && !force) {
    await bindCurrentRecommendationCards();
    return;
  }

  const version = ++forYouRequestVersion;
  const generation = authGeneration;
  forYouFeed.loading = true;
  forYouFeed.error = false;
  resetRecommendationObservation();

  try {
    const articles = await getArticleRecommendations(50);
    if (!isCurrentForYouRequest(version, generation)) {
      return;
    }
    forYouFeed.items = articles
      .filter((article) => !feedStore.isArticleDeleted(article.id))
      .map((article) => ({
        article,
        post: recommendationToFeedPost(article),
      }));
    forYouFeed.loaded = true;
    const likeGeneration = homeLikeGeneration;
    void hydrateHomeLikeStates(
      forYouFeed.items.map((item) => item.post.id),
      () => isCurrentForYouRequest(version, generation) && homeLikeGeneration === likeGeneration,
    );
    forYouFeed.loading = false;
    await bindCurrentRecommendationCards();
  } catch {
    if (!isCurrentForYouRequest(version, generation)) {
      return;
    }
    forYouFeed.error = true;
  } finally {
    if (version === forYouRequestVersion && generation === authGeneration) {
      forYouFeed.loading = false;
    }
  }
};

const loadActiveFeed = async (tab: FeedTab = activeTab.value) => {
  if (tab === 'for-you') {
    await loadForYou();
    return;
  }
  await loadFollowing();
};

const retryActiveFeed = () => {
  if (activeTab.value === 'for-you') {
    void loadForYou(true);
  } else {
    void loadFollowing(true);
  }
};

const bindRecommendationCard = (
  element: Element | ComponentPublicInstance | null,
  item: RecommendationFeedItem,
) => {
  if (element instanceof HTMLElement) {
    recommendationCardElements.set(item.article.id, element);
    recommendationTelemetry.observeFeedCard(element, item.article.id, item.article.tracking);
    return;
  }

  recommendationCardElements.delete(item.article.id);
  recommendationTelemetry.detachFeedCard(item.article.id, item.article.tracking);
  queueMicrotask(() => {
    if (recommendationCardElements.has(item.article.id)) {
      return;
    }
    const stillRendered = visibleForYouItems.value.some(
      visibleItem => visibleItem.article.id === item.article.id,
    );
    if (!stillRendered) {
      recommendationTelemetry.unobserveFeedCard(item.article.id, item.article.tracking);
    }
  });
};

const handleRecommendationClick = (article: RecommendedArticle) => {
  savePendingRecommendationAttribution(article.id, article.tracking);
  recommendationTelemetry.recordClick(article.id, article.tracking);
};

const removeHomeArticle = (articleId: number) => {
  const recommendationItem = forYouFeed.items.find(item => item.article.id === articleId);
  if (recommendationItem) {
    recommendationTelemetry.unobserveFeedCard(
      recommendationItem.article.id,
      recommendationItem.article.tracking,
    );
  }
  followingFeed.items = followingFeed.items.filter((post) => post.id !== articleId);
  forYouFeed.items = forYouFeed.items.filter((item) => item.post.id !== articleId);
  followingLoadedArticleIds.delete(articleId);
  recommendationCardElements.delete(articleId);
  likePendingArticleIds.delete(articleId);
  likeMutationVersions.delete(articleId);
};

const handleDeletePost = async (articleId: number) => {
  if (pendingDeleteArticleIds.has(articleId)) {
    return;
  }

  const ownerUserID = currentViewerID();
  if (
    ownerUserID === null
    || !authStore.isAuthenticated
    || !findHomePost(articleId)
    || !canDeletePost(findHomePost(articleId) as FeedPost)
  ) {
    return;
  }

  const capturedAuthGeneration = authGeneration;
  pendingDeleteArticleIds.add(articleId);
  deleteErrors.delete(articleId);

  const isCurrentDelete = () =>
    authStore.isAuthenticated
    && authGeneration === capturedAuthGeneration
    && currentViewerID() === ownerUserID
    && feedStore.viewerID === ownerUserID
    && pendingDeleteArticleIds.has(articleId);

  const finishTerminalDelete = () => {
    if (!isCurrentDelete() || !feedStore.markArticleDeleted(articleId, ownerUserID)) {
      return false;
    }
    removeHomeArticle(articleId);
    pendingDeleteArticleIds.delete(articleId);
    deleteErrors.delete(articleId);
    return true;
  };

  try {
    await deleteArticle(articleId);
    finishTerminalDelete();
  } catch (error) {
    if (!isCurrentDelete()) {
      return;
    }

    const status = getLikeErrorStatus(error);
    if (status === 404) {
      finishTerminalDelete();
      return;
    }
    deleteErrors.set(
      articleId,
      status === 403
        ? 'You can only delete your own posts.'
        : status === 401
          ? 'Please log in again to delete this post.'
          : 'Could not delete post. Please try again.',
    );
    pendingDeleteArticleIds.delete(articleId);
  }
};

const handleNotInterested = (articleId: number) => {
  const item = forYouFeed.items.find((feedItem) => feedItem.article.id === articleId);
  if (!item) {
    return;
  }

  recommendationTelemetry.recordNotInterested(item.article.id, item.article.tracking);
  recommendationTelemetry.unobserveFeedCard(item.article.id, item.article.tracking);
  forYouFeed.items = forYouFeed.items.filter((feedItem) => feedItem.article.id !== articleId);
  recommendationCardElements.delete(articleId);
};

watch(
  () => route.query.tab,
  normalizeRouteTab,
  { immediate: true },
);

watch(
  activeTab,
  (tab, previousTab) => {
    if (previousTab === 'for-you' && tab !== 'for-you') {
      resetRecommendationObservation();
    }
    if (previousTab === 'following' && tab !== 'following') {
      disconnectFollowingObserver();
    }
    void loadActiveFeed(tab);
  },
  { immediate: true },
);

watch(
  [
    activeTab,
    () => followingFeed.nextCursor,
    () => followingFeed.loadingMore,
    () => followingFeed.loadMoreError,
    () => followingFeed.loading,
  ],
  () => {
    void nextTick(updateFollowingObserver);
  },
  { flush: 'post' },
);

watch(
  () => authStore.currentIdentity?.id,
  (viewerID, previousViewerID) => {
    if (viewerID === previousViewerID) {
      return;
    }
    invalidateAfterLogout();
    if (authStore.isAuthenticated) {
      void loadActiveFeed();
    }
  },
);

watch(
  () => authStore.isAuthenticated,
  (isAuthenticated) => {
    if (!isAuthenticated) {
      invalidateAfterLogout();
      return;
    }
    void loadActiveFeed();
  },
  { immediate: true },
);

watch(
  () => feedStore.recentlyPublishedPosts
    .map((post) => String(post.id) + ':' + post.likeStatus)
    .join(','),
  () => {
    const articleIds = feedStore.recentlyPublishedPosts
      .filter((post) => post.likeStatus === 'unknown')
      .map((post) => post.id);
    if (!authStore.isAuthenticated || articleIds.length === 0) {
      return;
    }

    const likeGeneration = homeLikeGeneration;
    const viewerID = feedStore.viewerID;
    void hydrateHomeLikeStates(
      articleIds,
      () => authStore.isAuthenticated
        && feedStore.viewerID === viewerID
        && homeLikeGeneration === likeGeneration,
    );
  },
  { immediate: true },
);

onBeforeUnmount(() => {
  authGeneration += 1;
  followingRequestVersion += 1;
  followingPagingVersion += 1;
  disconnectFollowingObserver();
  invalidateHomeLikeWork();
  pendingDeleteArticleIds.clear();
  deleteErrors.clear();
  recommendationTelemetry.resetObservedCards();
  void recommendationTelemetry.flush(false);
  recommendationCardElements.clear();
});
</script>

<style scoped>
.home-view {
  min-height: 100vh;
  background: var(--color-surface);
  color: var(--color-text);
}

.home-feed-header {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

@media (max-width: 799px) {
  .home-feed-header {
    top: var(--app-mobile-nav-offset, 0px);
  }
}

.home-feed-header h1 {
  margin: 0;
  font-size: 22px;
  letter-spacing: -0.02em;
}

.home-feed-body {
  min-width: 0;
}

.home-feed-panel,
.feed-list {
  min-width: 0;
}

.recommendation-card-wrapper {
  min-width: 0;
}

.home-state {
  display: grid;
  justify-items: center;
  gap: var(--space-5);
  max-width: 480px;
  margin: 0 auto;
  padding: 72px var(--space-5);
  text-align: center;
}

.home-state h2 {
  margin: 0;
  color: var(--color-text);
  font-size: 24px;
  line-height: 1.25;
}

.home-state__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: var(--space-3);
}

.home-state__primary,
.home-state__secondary {
  display: inline-flex;
  min-height: 40px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-5);
  font-size: 14px;
  font-weight: 750;
  text-decoration: none;
}

.home-state__primary {
  border: 1px solid var(--color-accent);
  background: var(--color-accent);
  color: #fff;
}

.home-state__secondary {
  border: 1px solid var(--color-border-strong);
  background: var(--color-surface);
  color: var(--color-text);
}

.home-state__primary:hover,
.home-state__primary:focus-visible,
.home-state__secondary:hover,
.home-state__secondary:focus-visible {
  border-color: var(--color-accent);
}

.feed-skeleton {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
}

.feed-skeleton span {
  display: block;
  height: 12px;
  border-radius: var(--radius-sm);
  background: var(--color-surface-subtle);
  animation: feed-shimmer 1.2s ease-in-out infinite;
}

.feed-skeleton__author {
  width: 132px;
}

.feed-skeleton__title {
  width: 74%;
  height: 20px !important;
}

.feed-skeleton__line {
  width: 92%;
}

.feed-skeleton__line--short {
  width: 58% !important;
}

@keyframes feed-shimmer {
  0%,
  100% {
    opacity: 0.55;
  }
  50% {
    opacity: 1;
  }
}

.home-feed-inline-state {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  min-height: 56px;
  padding: var(--space-3) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-secondary);
  font-size: 13px;
}

.home-feed-sentinel {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  min-height: 64px;
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-secondary);
  font-size: 13px;
}

@media (max-width: 620px) {
  .home-feed-header {
    align-items: stretch;
    flex-direction: column;
    padding: var(--space-3) var(--space-4);
  }

  .home-feed-header h1 {
    font-size: 20px;
  }

  .home-state {
    padding-inline: var(--space-4);
  }
}

@media (prefers-reduced-motion: reduce) {
  .feed-skeleton span {
    animation: none;
  }
}
</style>
