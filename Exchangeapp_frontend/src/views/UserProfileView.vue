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
            @toggle-like="handleLikeToggle"
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
import { getUser, getUserArticles } from '../services/userService';
import { getArticleLikeStates, likeArticle, unlikeArticle } from '../services/likeService';
import type { Article } from '../types/Article';
import type { FeedLikeStateUpdate, FeedPost } from '../types/Feed';
import type { PublicUser } from '../types/User';
import {
  applyFeedLikeStateUpdate,
  articleToFeedPost,
  setFeedPostLikeUnavailable,
} from '../utils/feedPost';

const pageSize = 20;
const skeletonCount = 3;
const route = useRoute();
const router = useRouter();

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
let profileLikeHydrationGeneration = 0;
let observer: IntersectionObserver | null = null;

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
      return true;
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

onMounted(() => {
  void nextTick(updateObserver);
});

onBeforeUnmount(() => {
  profileRequestVersion += 1;
  feedRequestVersion += 1;
  profileLikeHydrationGeneration += 1;
  likePendingArticleIds.clear();
  likeMutationVersions.clear();
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
  min-width: 0;
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