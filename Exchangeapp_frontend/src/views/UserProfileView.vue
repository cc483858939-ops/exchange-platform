<template>
  <main class="profile-view">
    <header class="profile-header">
      <button
        class="profile-header__back"
        type="button"
        aria-label="Back"
        @click="goBack"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="m15 18-6-6 6-6" />
        </svg>
        <span class="profile-header__copy">
          <strong>{{ headerUsername }}</strong>
          <small>Profile</small>
        </span>
      </button>
    </header>

    <section
      v-if="profileLoading"
      class="profile-identity profile-identity--loading"
      aria-live="polite"
      aria-label="Loading profile"
    >
      <span class="profile-skeleton profile-skeleton--avatar" aria-hidden="true"></span>
      <span class="profile-skeleton-copy" aria-hidden="true">
        <span class="profile-skeleton profile-skeleton--name"></span>
        <span class="profile-skeleton profile-skeleton--handle"></span>
      </span>
    </section>

    <section
      v-else-if="user"
      class="profile-identity"
      aria-labelledby="profile-name"
    >
      <span class="profile-avatar" aria-hidden="true">{{ profileInitial }}</span>
      <div class="profile-identity__copy">
        <h1 id="profile-name">{{ user.username }}</h1>
        <p class="profile-identity__handle">@{{ user.username }}</p>
        <time
          v-if="joinedLabel"
          class="profile-identity__joined"
          :datetime="user.created_at"
        >
          Joined {{ joinedLabel }}
        </time>
        <div v-if="socialReady" class="profile-social" aria-label="Social stats">
          <span>{{ followState?.following_count ?? 0 }} Following</span>
          <span>{{ followState?.follower_count ?? 0 }} Followers</span>
        </div>
        <div v-else-if="followLoading" class="profile-social profile-social--loading" aria-label="Loading social stats">
          <span class="profile-social__skeleton"></span>
          <span class="profile-social__skeleton"></span>
        </div>
        <p v-if="followError" class="profile-social-error" aria-live="polite">
          <span>Social stats unavailable.</span>
          <button class="profile-action profile-action--compact" type="button" @click="retryFollowState">
            Retry
          </button>
        </p>
      </div>
      <div v-if="showFollowControl" class="profile-identity__action">
        <button
          class="profile-follow-button"
          :class="{ 'profile-follow-button--following': followState?.following }"
          type="button"
          :aria-pressed="followState?.following === true"
          :aria-busy="followPending"
          :disabled="followPending"
          @click="handleFollowToggle"
        >
          {{ followState?.following ? 'Following' : 'Follow' }}
        </button>
        <p v-if="followActionError" class="profile-action-error" aria-live="polite">
          {{ followActionError }}
        </p>
      </div>
    </section>

    <section
      v-else
      class="profile-state profile-state--page"
      aria-live="polite"
      role="alert"
    >
      <h1>{{ profileNotFound ? 'Profile not found.' : 'Profile could not be loaded.' }}</h1>
      <p>{{ profileNotFound ? 'This user does not exist.' : profileError }}</p>
      <div class="profile-state__actions">
        <button
          v-if="profileNotFound"
          class="profile-action"
          type="button"
          @click="goHome"
        >
          Back to Home
        </button>
        <button
          v-else
          class="profile-action"
          type="button"
          @click="retryProfile"
        >
          Retry
        </button>
      </div>
    </section>

    <template v-if="user">
      <nav class="profile-tabs" aria-label="Profile sections">
        <span class="profile-tab profile-tab--active">Posts</span>
      </nav>

      <section class="profile-posts" aria-labelledby="profile-posts-heading">
        <h2 id="profile-posts-heading" class="sr-only">Posts</h2>

        <div
          v-if="articlesInitialLoading"
          class="profile-skeleton-list"
          aria-live="polite"
          aria-label="Loading posts"
        >
          <div v-for="slot in skeletonCount" :key="slot" class="profile-skeleton-post">
            <span class="profile-skeleton profile-skeleton--identity" aria-hidden="true"></span>
            <span class="profile-skeleton-post__copy" aria-hidden="true">
              <span class="profile-skeleton profile-skeleton--title"></span>
              <span class="profile-skeleton profile-skeleton--excerpt"></span>
              <span class="profile-skeleton profile-skeleton--metric"></span>
            </span>
          </div>
        </div>

        <div
          v-else-if="articlesInitialError"
          class="profile-state profile-state--inline"
          role="alert"
        >
          <p>Posts could not be loaded.</p>
          <button class="profile-action" type="button" @click="retryInitialPosts">
            Retry posts
          </button>
        </div>

        <p v-else-if="articles.length === 0" class="profile-empty">
          No posts yet.
        </p>

        <div v-else class="profile-post-list">
          <PostCard
            v-for="post in articles"
            :key="post.id"
            :post="post"
            :like-pending="likePendingArticleIds.has(post.id)"
            :show-delete="canDeletePost(post)"
            :delete-pending="pendingDeleteArticleIds.has(post.id)"
            :delete-error="deleteErrors.get(post.id) || ''"
            @toggle-like="handleLikeToggle"
            @delete-post="handleDeletePost"
          />
        </div>

        <div
          v-if="hasMore || articlesLoadingMore || articlesLoadMoreError"
          ref="sentinelRef"
          class="profile-feed-sentinel"
          aria-live="polite"
        >
          <span v-if="articlesLoadingMore">Loading more posts...</span>

          <template v-else-if="articlesLoadMoreError">
            <span>Could not load more posts.</span>
            <button class="profile-action" type="button" @click="retryLoadMore">
              Retry
            </button>
          </template>

          <button
            v-else-if="!intersectionObserverAvailable && hasMore"
            class="profile-action"
            type="button"
            @click="loadMorePosts"
          >
            Load more posts
          </button>
        </div>
      </section>
    </template>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import PostCard from '../components/feed/PostCard.vue';
import { useAuthStore } from '../store/auth';
import { useFeedStore } from '../store/feed';
import { followUser, getUser, getUserArticles, getUserFollowState, unfollowUser } from '../services/userService';
import { deleteArticle } from '../services/articleService';
import { getArticleLikeStates, likeArticle, unlikeArticle } from '../services/likeService';
import type { Article } from '../types/Article';
import type { FeedLikeStateUpdate, FeedPost } from '../types/Feed';
import type { PublicUser } from '../types/User';
import type { UserFollowState } from '../services/userService';
import {
  applyFeedLikeStateUpdate,
  articleToFeedPost,
  setFeedPostLikeUnavailable,
} from '../utils/feedPost';

const pageSize = 20;
const skeletonCount = 3;
const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const feedStore = useFeedStore();

const userId = computed(() => String(route.params.id ?? '').trim());
const user = ref<PublicUser | null>(null);
const profileLoading = ref(false);
const profileError = ref('');
const profileNotFound = ref(false);

const articles = ref<FeedPost[]>([]);
const articlesInitialLoading = ref(false);
const articlesLoadingMore = ref(false);
const articlesInitialError = ref('');
const articlesLoadMoreError = ref('');
const nextOffset = ref(0);
const hasMore = ref(false);

const sentinelRef = ref<HTMLElement | null>(null);
const intersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
const loadedArticleIds = new Set<number>();
let profileRequestVersion = 0;
let feedRequestVersion = 0;
const likePendingArticleIds = reactive(new Set<number>());
const likeMutationVersions = new Map<number, number>();
const pendingDeleteArticleIds = reactive(new Set<number>());
const deleteErrors = reactive(new Map<number, string>());
let profileLikeHydrationGeneration = 0;
let observer: IntersectionObserver | null = null;
const followState = ref<UserFollowState | null>(null);
const followLoading = ref(false);
const followError = ref('');
const followActionError = ref('');
const followPending = ref(false);
let followRequestVersion = 0;
let followMutationVersion = 0;
let viewerGeneration = 0;

const headerUsername = computed(() => user.value?.username || 'Profile');
const profileInitial = computed(
  () => Array.from(user.value?.username.trim() ?? '')[0]?.toUpperCase() || '?',
);
const joinedLabel = computed(() => {
  const value = user.value?.created_at;
  if (!value) {
    return '';
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return '';
  }

  return date.toLocaleDateString(undefined, {
    month: 'long',
    year: 'numeric',
  });
});

const getErrorStatus = (error: unknown) =>
  (error as { response?: { status?: number } }).response?.status;

const currentViewerID = computed(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && id > 0 ? id : null;
});

const socialReady = computed(() => Boolean(
  user.value
  && followState.value
  && !followLoading.value
  && !followError.value,
));

const showFollowControl = computed(() => Boolean(
  socialReady.value
  && currentViewerID.value !== null
  && user.value
  && user.value.id !== currentViewerID.value,
));

const canDeletePost = (post: FeedPost) =>
  authStore.isAuthenticated
  && currentViewerID.value !== null
  && post.author.id === currentViewerID.value;

const invalidateSocialState = () => {
  followRequestVersion += 1;
  followMutationVersion += 1;
  followState.value = null;
  followLoading.value = false;
  followError.value = '';
  followActionError.value = '';
  followPending.value = false;
};

const isCurrentSocialRequest = (
  profileVersion: number,
  targetID: number,
  viewerID: number,
  capturedViewerGeneration: number,
  requestVersion: number,
  mutationVersion: number,
) =>
  profileVersion === profileRequestVersion
  && user.value?.id === targetID
  && currentViewerID.value === viewerID
  && viewerGeneration === capturedViewerGeneration
  && followRequestVersion === requestVersion
  && followMutationVersion === mutationVersion
  && authStore.isAuthenticated;

const loadFollowStateForProfile = async (targetID: number, profileVersion: number) => {
  const viewerID = currentViewerID.value;
  if (viewerID === null || !authStore.isAuthenticated) {
    followState.value = null;
    followLoading.value = false;
    return;
  }

  const requestVersion = ++followRequestVersion;
  const mutationVersion = followMutationVersion;
  const capturedViewerGeneration = viewerGeneration;
  followLoading.value = true;
  followError.value = '';
  followActionError.value = '';

  const isCurrent = () => isCurrentSocialRequest(
    profileVersion,
    targetID,
    viewerID,
    capturedViewerGeneration,
    requestVersion,
    mutationVersion,
  );

  try {
    const response = await getUserFollowState(targetID);
    if (!isCurrent()) {
      return;
    }
    if (response.user_id !== targetID) {
      throw new Error('invalid follow response');
    }
    followState.value = response;
  } catch {
    if (isCurrent()) {
      followState.value = null;
      followError.value = 'Social stats unavailable.';
    }
  } finally {
    if (isCurrent()) {
      followLoading.value = false;
    }
  }
};

const retryFollowState = () => {
  if (user.value && currentViewerID.value !== null) {
    void loadFollowStateForProfile(user.value.id, profileRequestVersion);
  }
};

const handleFollowToggle = async () => {
  const targetID = user.value?.id;
  const viewerID = currentViewerID.value;
  const previous = followState.value;
  if (
    targetID === undefined
    || viewerID === null
    || targetID === viewerID
    || !previous
    || !socialReady.value
    || followPending.value
  ) {
    return;
  }

  const profileVersion = profileRequestVersion;
  const requestVersion = followRequestVersion;
  const capturedViewerGeneration = viewerGeneration;
  const mutationVersion = ++followMutationVersion;
  const previousState: UserFollowState = { ...previous };
  followPending.value = true;
  followActionError.value = '';
  followState.value = {
    ...previousState,
    following: !previousState.following,
    follower_count: previousState.following
      ? Math.max(0, previousState.follower_count - 1)
      : previousState.follower_count + 1,
  };

  const isCurrent = () =>
    profileVersion === profileRequestVersion
    && user.value?.id === targetID
    && currentViewerID.value === viewerID
    && viewerGeneration === capturedViewerGeneration
    && followRequestVersion === requestVersion
    && followMutationVersion === mutationVersion
    && followPending.value
    && authStore.isAuthenticated;

  try {
    const response = previousState.following
      ? await unfollowUser(targetID)
      : await followUser(targetID);
    if (!isCurrent()) {
      return;
    }
    if (response.user_id !== targetID) {
      throw new Error('invalid follow response');
    }
    followState.value = response;
    followPending.value = false;
    followActionError.value = '';
  } catch {
    if (!isCurrent()) {
      return;
    }
    followState.value = previousState;
    followPending.value = false;
    followActionError.value = 'Could not update follow status.';
  }
};

const getLikeMutationVersion = (articleId: number) =>
  likeMutationVersions.get(articleId) ?? 0;

const bumpLikeMutationVersion = (articleId: number) => {
  const nextVersion = getLikeMutationVersion(articleId) + 1;
  likeMutationVersions.set(articleId, nextVersion);
  return nextVersion;
};


const findProfilePost = (articleId: number) =>
  articles.value.find((post) => post.id === articleId);

const applyProfileLikeUpdate = (update: FeedLikeStateUpdate, expectedVersion?: number) => {
  if (
    expectedVersion !== undefined
    && getLikeMutationVersion(update.articleId) !== expectedVersion
  ) {
    return false;
  }

  const post = findProfilePost(update.articleId);
  return post ? applyFeedLikeStateUpdate(post, update) : false;
};

const markProfileHydrationUnavailable = (articleIds: number[], versions: Map<number, number>) => {
  articleIds.forEach((articleId) => {
    const capturedVersion = versions.get(articleId);
    const post = findProfilePost(articleId);
    if (
      capturedVersion === undefined
      || getLikeMutationVersion(articleId) !== capturedVersion
      || !post
      || post.likeStatus !== 'unknown'
    ) {
      return;
    }
    setFeedPostLikeUnavailable(post);
  });
};

const hydrateProfileLikeStates = async (newPosts: FeedPost[], profileVersion: number) => {
  const articleIds = Array.from(new Set(newPosts.map((post) => post.id)));
  if (articleIds.length === 0) {
    return;
  }

  const likeGeneration = profileLikeHydrationGeneration;
  const versions = new Map(
    articleIds.map((articleId) => [articleId, getLikeMutationVersion(articleId)]),
  );

  try {
    const response = await getArticleLikeStates(articleIds);
    if (
      profileVersion !== profileRequestVersion
      || likeGeneration !== profileLikeHydrationGeneration
      || userId.value !== String(route.params.id ?? '').trim()
    ) {
      return;
    }

    const readyIds = new Set<number>();
    response.items.forEach((item) => {
      const capturedVersion = versions.get(item.article_id);
      if (
        capturedVersion === undefined
        || getLikeMutationVersion(item.article_id) !== capturedVersion
        || !findProfilePost(item.article_id)
      ) {
        return;
      }

      readyIds.add(item.article_id);
      applyProfileLikeUpdate({
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
        && findProfilePost(articleId)
      ) {
        applyProfileLikeUpdate({
          articleId,
          likes: 0,
          liked: false,
          status: 'unavailable',
        }, capturedVersion);
      }
    });
  } catch {
    if (
      profileVersion === profileRequestVersion
      && likeGeneration === profileLikeHydrationGeneration
      && userId.value === String(route.params.id ?? '').trim()
    ) {
      markProfileHydrationUnavailable(articleIds, versions);
    }
  }
};
const disconnectObserver = () => {
  observer?.disconnect();
  observer = null;
};

const resetFeedState = () => {
  feedRequestVersion += 1;
  profileLikeHydrationGeneration += 1;
  likePendingArticleIds.clear();
  likeMutationVersions.clear();
  pendingDeleteArticleIds.clear();
  deleteErrors.clear();
  articles.value = [];
  articlesInitialLoading.value = false;
  articlesLoadingMore.value = false;
  articlesInitialError.value = '';
  articlesLoadMoreError.value = '';
  nextOffset.value = 0;
  hasMore.value = false;
  loadedArticleIds.clear();
  disconnectObserver();
};

const appendArticles = (rawArticles: Article[]): FeedPost[] => {
  const newPosts = rawArticles
    .filter((article) => {
      if (loadedArticleIds.has(article.ID)) {
        return false;
      }

      loadedArticleIds.add(article.ID);
      return !feedStore.isArticleDeleted(article.ID);
    })
    .map(articleToFeedPost);

  if (newPosts.length > 0) {
    articles.value = [...articles.value, ...newPosts];
  }
  return newPosts;
};

const loadInitialPosts = async (id: string, profileVersion: number) => {
  const feedVersion = ++feedRequestVersion;
  articlesInitialLoading.value = true;
  articlesInitialError.value = '';
  articlesLoadMoreError.value = '';
  articles.value = [];
  nextOffset.value = 0;
  hasMore.value = false;
  loadedArticleIds.clear();
  disconnectObserver();

  try {
    const rawPage = await getUserArticles(id, { limit: pageSize, offset: 0 });
    if (profileVersion !== profileRequestVersion || feedVersion !== feedRequestVersion) {
      return;
    }

    const newPosts = appendArticles(rawPage);
    nextOffset.value += rawPage.length;
    hasMore.value = rawPage.length === pageSize;
    void hydrateProfileLikeStates(newPosts, profileVersion);
  } catch (error) {
    if (profileVersion !== profileRequestVersion || feedVersion !== feedRequestVersion) {
      return;
    }

    articlesInitialError.value = getErrorStatus(error) === 404
      ? 'The user posts could not be found.'
      : "Try again to load this user's posts.";
  } finally {
    if (profileVersion === profileRequestVersion && feedVersion === feedRequestVersion) {
      articlesInitialLoading.value = false;
    }
  }
};

const loadMorePosts = async () => {
  const id = userId.value;
  const profileVersion = profileRequestVersion;

  if (
    !id
    || !user.value
    || !hasMore.value
    || articlesInitialLoading.value
    || articlesLoadingMore.value
    || articlesLoadMoreError.value
  ) {
    return;
  }

  const offset = nextOffset.value;
  const feedVersion = ++feedRequestVersion;
  articlesLoadingMore.value = true;
  articlesLoadMoreError.value = '';

  try {
    const rawPage = await getUserArticles(id, { limit: pageSize, offset });
    if (profileVersion !== profileRequestVersion || feedVersion !== feedRequestVersion) {
      return;
    }

    const newPosts = appendArticles(rawPage);
    nextOffset.value += rawPage.length;
    hasMore.value = rawPage.length === pageSize;
    void hydrateProfileLikeStates(newPosts, profileVersion);
  } catch (error) {
    if (profileVersion !== profileRequestVersion || feedVersion !== feedRequestVersion) {
      return;
    }

    articlesLoadMoreError.value = getErrorStatus(error) === 404
      ? 'The user posts could not be found.'
      : 'Try again to load more posts.';
  } finally {
    if (profileVersion === profileRequestVersion && feedVersion === feedRequestVersion) {
      articlesLoadingMore.value = false;
    }
  }
};

const retryInitialPosts = () => {
  if (user.value && userId.value) {
    resetFeedState();
    void loadInitialPosts(userId.value, profileRequestVersion);
  }
};

const retryLoadMore = () => {
  articlesLoadMoreError.value = '';
  void loadMorePosts();
};

const retryProfile = () => {
  void loadProfile();
};

const handleDeletePost = async (articleId: number) => {
  if (pendingDeleteArticleIds.has(articleId)) {
    return;
  }

  const ownerUserID = currentViewerID.value;
  const post = findProfilePost(articleId);
  if (
    ownerUserID === null
    || !authStore.isAuthenticated
    || !post
    || !canDeletePost(post)
  ) {
    return;
  }

  const capturedViewerGeneration = viewerGeneration;
  const capturedProfileID = userId.value;
  pendingDeleteArticleIds.add(articleId);
  deleteErrors.delete(articleId);

  const isCurrentDelete = () =>
    authStore.isAuthenticated
    && currentViewerID.value === ownerUserID
    && viewerGeneration === capturedViewerGeneration
    && userId.value === capturedProfileID
    && pendingDeleteArticleIds.has(articleId);

  const finishTerminalDelete = () => {
    if (!isCurrentDelete() || !feedStore.markArticleDeleted(articleId, ownerUserID)) {
      return false;
    }

    const wasLoaded = articles.value.some((item) => item.id === articleId);
    articles.value = articles.value.filter((item) => item.id !== articleId);
    loadedArticleIds.delete(articleId);
    if (wasLoaded) {
      nextOffset.value = Math.max(0, nextOffset.value - 1);
    }
    pendingDeleteArticleIds.delete(articleId);
    deleteErrors.delete(articleId);
    likePendingArticleIds.delete(articleId);
    likeMutationVersions.delete(articleId);
    feedRequestVersion += 1;
    articlesLoadingMore.value = false;
    articlesLoadMoreError.value = '';
    disconnectObserver();
    void nextTick(updateObserver);
    return true;
  };

  try {
    await deleteArticle(articleId);
    finishTerminalDelete();
  } catch (error) {
    if (!isCurrentDelete()) {
      return;
    }

    const status = getErrorStatus(error);
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

const handleLikeToggle = async (articleId: number) => {
  const post = findProfilePost(articleId);
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
  const likeGeneration = profileLikeHydrationGeneration;
  const profileVersion = profileRequestVersion;
  const profileID = userId.value;
  likePendingArticleIds.add(articleId);

  applyProfileLikeUpdate({
    articleId,
    likes: previousLiked ? Math.max(0, previousLikes - 1) : previousLikes + 1,
    liked: !previousLiked,
    status: 'ready',
  }, mutationVersion);

  const isCurrentMutation = () =>
    profileRequestVersion === profileVersion
    && profileLikeHydrationGeneration === likeGeneration
    && userId.value === profileID
    && getLikeMutationVersion(articleId) === mutationVersion
    && likePendingArticleIds.has(articleId);

  try {
    const result = previousLiked
      ? await unlikeArticle(articleId)
      : await likeArticle(articleId);

    if (isCurrentMutation()) {
      applyProfileLikeUpdate({
        articleId,
        likes: result.likes,
        liked: result.liked,
        status: 'ready',
      }, mutationVersion);
      likePendingArticleIds.delete(articleId);
    }
  } catch (error) {
    if (!isCurrentMutation()) {
      return;
    }

    applyProfileLikeUpdate({
      articleId,
      likes: previousLikes,
      liked: previousLiked,
      status: 'ready',
    }, mutationVersion);

    if (getErrorStatus(error) === 503) {
      applyProfileLikeUpdate({
        articleId,
        likes: previousLikes,
        liked: previousLiked,
        status: 'unavailable',
      }, mutationVersion);
    }
    likePendingArticleIds.delete(articleId);
  }
};

const loadProfile = async () => {
  const id = userId.value;
  const profileVersion = ++profileRequestVersion;
  invalidateSocialState();

  user.value = null;
  profileLoading.value = false;
  profileError.value = '';
  profileNotFound.value = false;
  resetFeedState();

  if (!id) {
    profileError.value = 'This profile URL is not valid.';
    return;
  }

  profileLoading.value = true;

  try {
    const loadedUser = await getUser(id);
    if (profileVersion !== profileRequestVersion) {
      return;
    }

    user.value = loadedUser;
    profileLoading.value = false;
    void loadInitialPosts(id, profileVersion);
    void loadFollowStateForProfile(loadedUser.id, profileVersion);
  } catch (error) {
    if (profileVersion !== profileRequestVersion) {
      return;
    }

    profileNotFound.value = getErrorStatus(error) === 404;
    profileError.value = profileNotFound.value
      ? ''
      : 'The profile could not be loaded. Try again.';
  } finally {
    if (profileVersion === profileRequestVersion) {
      profileLoading.value = false;
    }
  }
};

const goHome = () => {
  void router.push({ name: 'Home' });
};

const goBack = () => {
  const historyState = window.history.state as { back?: string | null } | null;
  if (historyState?.back) {
    router.back();
    return;
  }

  goHome();
};

const updateObserver = () => {
  disconnectObserver();

  if (
    !intersectionObserverAvailable
    || !sentinelRef.value
    || !hasMore.value
    || articlesLoadingMore.value
    || articlesLoadMoreError.value
    || !user.value
  ) {
    return;
  }

  observer = new IntersectionObserver((entries) => {
    if (entries.some((entry) => entry.isIntersecting)) {
      void loadMorePosts();
    }
  }, { rootMargin: '240px 0px' });

  observer.observe(sentinelRef.value);
};

watch(
  [
    userId,
    () => user.value,
    () => hasMore.value,
    () => articlesLoadingMore.value,
    () => articlesLoadMoreError.value,
    () => articlesInitialLoading.value,
  ],
  () => {
    void nextTick(updateObserver);
  },
  { flush: 'post' },
);

watch(userId, () => {
  void loadProfile();
}, { immediate: true });

watch(currentViewerID, (viewerID, previousViewerID) => {
  if (viewerID === previousViewerID) {
    return;
  }
  viewerGeneration += 1;
  pendingDeleteArticleIds.clear();
  deleteErrors.clear();
  invalidateSocialState();
  if (viewerID !== null && user.value) {
    void loadFollowStateForProfile(user.value.id, profileRequestVersion);
  }
});

onMounted(() => {
  void nextTick(updateObserver);
});

onBeforeUnmount(() => {
  profileRequestVersion += 1;
  feedRequestVersion += 1;
  profileLikeHydrationGeneration += 1;
  viewerGeneration += 1;
  invalidateSocialState();
  likePendingArticleIds.clear();
  likeMutationVersions.clear();
  pendingDeleteArticleIds.clear();
  deleteErrors.clear();
  disconnectObserver();
});
</script>

<style scoped>
.profile-view {
  min-height: 100vh;
  color: var(--color-text);
  background: var(--color-surface);
}

.profile-header {
  position: sticky;
  top: 0;
  z-index: 12;
  display: flex;
  align-items: center;
  min-height: 56px;
  padding: var(--space-2) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.profile-header__back {
  display: inline-flex;
  align-items: center;
  gap: var(--space-3);
  min-width: 0;
  border: 0;
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  text-align: left;
}

.profile-header__back:hover,
.profile-header__back:focus-visible {
  background: var(--color-surface-subtle);
}

.profile-header__back svg {
  width: 22px;
  height: 22px;
  flex: 0 0 auto;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2;
}

.profile-header__copy {
  display: grid;
  min-width: 0;
  gap: 1px;
}

.profile-header__copy strong,
.profile-header__copy small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.profile-header__copy strong {
  font-size: 15px;
  line-height: 1.1;
}

.profile-header__copy small {
  color: var(--color-text-secondary);
  font-size: 12px;
  line-height: 1.1;
}

.profile-identity {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-4);
  padding: var(--space-6) var(--space-5) var(--space-5);
  border-bottom: 1px solid var(--color-border);
}

.profile-avatar {
  display: grid;
  width: 76px;
  height: 76px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid var(--color-border-strong);
  border-radius: 50%;
  background: var(--color-surface-subtle);
  color: var(--color-text-secondary);
  font-size: 28px;
  font-weight: 800;
}

.profile-identity__copy {
  flex: 1 1 220px;
  min-width: 0;
}

.profile-identity__action {
  display: grid;
  flex: 0 0 auto;
  justify-items: end;
  gap: var(--space-2);
  margin-left: auto;
}

.profile-social {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-4);
  margin-top: var(--space-3);
  color: var(--color-text-secondary);
  font-size: 13px;
  font-weight: 650;
}

.profile-social--loading {
  gap: var(--space-3);
}

.profile-social__skeleton {
  display: block;
  width: 76px;
  height: 14px;
  border-radius: var(--radius-sm);
  background: var(--color-surface-subtle);
}

.profile-social-error,
.profile-action-error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin: var(--space-3) 0 0;
  color: var(--color-text-secondary);
  font-size: 12px;
}

.profile-action-error {
  justify-content: end;
  max-width: 180px;
  color: var(--color-text-secondary);
  text-align: right;
}

.profile-action--compact {
  min-height: 32px;
  padding: var(--space-1) var(--space-3);
  font-size: 12px;
}

.profile-follow-button {
  min-height: 40px;
  border: 1px solid var(--color-accent);
  border-radius: var(--radius-pill);
  padding: 0 var(--space-5);
  background: var(--color-accent);
  color: #fff;
  cursor: pointer;
  font: inherit;
  font-size: 14px;
  font-weight: 750;
}

.profile-follow-button--following {
  border-color: var(--color-border-strong);
  background: var(--color-surface);
  color: var(--color-text);
}

.profile-follow-button:hover,
.profile-follow-button:focus-visible {
  border-color: var(--color-text);
}

.profile-follow-button:disabled {
  cursor: wait;
  opacity: 0.64;
}

.profile-identity h1 {
  margin: 0;
  overflow-wrap: anywhere;
  color: var(--color-text);
  font-size: 26px;
  line-height: 1.15;
  letter-spacing: -0.025em;
}

.profile-identity__handle,
.profile-identity__joined {
  margin: var(--space-1) 0 0;
  color: var(--color-text-secondary);
  font-size: 14px;
}

.profile-identity__joined {
  display: block;
  color: var(--color-text-tertiary);
  font-size: 13px;
}

.profile-tabs {
  display: flex;
  min-height: 52px;
  align-items: stretch;
  border-bottom: 1px solid var(--color-border);
}

.profile-tab {
  display: inline-flex;
  align-items: center;
  margin-inline: var(--space-5);
  border-bottom: 2px solid transparent;
  color: var(--color-text-secondary);
  font-size: 14px;
  font-weight: 750;
}

.profile-tab--active {
  border-bottom-color: var(--color-accent);
  color: var(--color-text);
}

.profile-post-list {
  border-top: 0;
}

.profile-skeleton-list {
  border-bottom: 1px solid var(--color-border);
}

.profile-skeleton-post {
  display: grid;
  grid-template-columns: 30px minmax(0, 1fr);
  gap: 9px;
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
}

.profile-skeleton-post:last-child {
  border-bottom: 0;
}

.profile-skeleton {
  display: block;
  border-radius: var(--radius-sm);
  background: var(--color-surface-subtle);
}

.profile-skeleton--avatar {
  width: 76px;
  height: 76px;
  border-radius: 50%;
}

.profile-skeleton-copy {
  display: grid;
  align-content: center;
  gap: var(--space-2);
  min-width: 0;
}

.profile-skeleton--name {
  width: min(220px, 70vw);
  height: 20px;
}

.profile-skeleton--handle {
  width: 120px;
  height: 14px;
}

.profile-skeleton--identity {
  width: 30px;
  height: 30px;
  border-radius: 50%;
}

.profile-skeleton-post__copy {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
}

.profile-skeleton--title {
  width: min(430px, 90%);
  height: 18px;
}

.profile-skeleton--excerpt {
  width: min(520px, 100%);
  height: 14px;
}

.profile-skeleton--metric {
  width: 90px;
  height: 13px;
}

.profile-state {
  padding: var(--space-8) var(--space-5);
  color: var(--color-text-secondary);
  text-align: center;
}

.profile-state--page {
  min-height: 260px;
  border-bottom: 1px solid var(--color-border);
}

.profile-state h1 {
  margin: 0;
  color: var(--color-text);
  font-size: 24px;
  letter-spacing: -0.02em;
}

.profile-state p {
  margin: var(--space-2) 0 0;
}

.profile-state__actions {
  display: flex;
  justify-content: center;
  gap: var(--space-2);
  margin-top: var(--space-4);
}

.profile-state--inline {
  padding-inline: var(--space-5);
}

.profile-state--inline p {
  margin: 0 0 var(--space-3);
}

.profile-action {
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-pill);
  padding: var(--space-2) var(--space-4);
  background: var(--color-surface);
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  font-weight: 700;
}

.profile-action:hover,
.profile-action:focus-visible {
  border-color: var(--color-accent);
  color: var(--color-accent);
}

.profile-empty {
  margin: 0;
  padding: var(--space-8) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-secondary);
  font-size: 15px;
}

.profile-feed-sentinel {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  min-height: 64px;
  padding: var(--space-4) var(--space-5);
  color: var(--color-text-secondary);
  font-size: 13px;
}

.profile-feed-sentinel .profile-action {
  flex: 0 0 auto;
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

@media (max-width: 799px) {
  .profile-header {
    top: var(--app-mobile-nav-offset, 0px);
  }
}

@media (max-width: 420px) {
  .profile-header {
    padding-inline: var(--space-3);
  }

  .profile-identity,
  .profile-skeleton-post,
  .profile-empty,
  .profile-state,
  .profile-feed-sentinel {
    padding-inline: var(--space-4);
  }

  .profile-identity {
    align-items: flex-start;
  }

  .profile-identity__action {
    flex-basis: 100%;
    justify-items: start;
    margin-left: 0;
  }

  .profile-action-error {
    justify-content: start;
    max-width: none;
    text-align: left;
  }

  .profile-avatar {
    width: 64px;
    height: 64px;
    font-size: 24px;
  }

  .profile-identity h1 {
    font-size: 22px;
  }

  .profile-tab {
    margin-inline: var(--space-4);
  }
}
</style>