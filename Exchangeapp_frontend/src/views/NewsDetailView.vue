<template>
  <main class="detail-view">
    <header class="detail-header">
      <button class="detail-header__back" type="button" aria-label="Back" @click="goBack">
        <AppIcon name="arrow-left" :size="20" />
        <span>Post</span>
      </button>
    </header>

    <section v-if="articleLoading" class="detail-state" aria-live="polite">
      <p>Loading article...</p>
    </section>

    <template v-else-if="article">
      <article class="article-detail">
        <AuthorIdentity :author="article.author" :created-at="article.CreatedAt" />

        <h1 v-if="article.title.trim()" class="article-detail__title">{{ article.title }}</h1>

        <p v-if="article.expired_at" class="article-detail__expiry">
          {{ articleExpiredLabel }}
        </p>

        <div ref="articleBodyRef" class="article-detail__body">{{ article.content }}</div>

        <figure v-if="showCover" class="article-detail__cover">
          <img
            :src="article.cover_image_url"
            :alt="article.title.trim() || 'Post image'"
            loading="lazy"
            @error="hideCover"
          />
        </figure>

        <div class="article-detail__meta">
          <span>{{ article.category || 'Article' }}</span>
          <span v-if="article.CreatedAt">{{ formatArticleDate(article.CreatedAt) }}</span>
          <button
            v-if="canDeleteArticle"
            class="detail-delete-action"
            type="button"
            :disabled="deletePending"
            :aria-busy="deletePending"
            @click="handleDeleteArticle"
          >
            <AppIcon name="trash" :size="16" />
            <span>Delete post</span>
          </button>
        </div>

        <div class="article-detail__engagement" aria-label="Article engagement">
          <button
            class="engagement-action"
            :class="{ 'engagement-action--liked': liked }"
            type="button"
            :disabled="!authStore.isAuthenticated || likeStateLoading || likeSubmitting"
            :aria-pressed="liked"
            :aria-busy="likeStateLoading || likeSubmitting"
            @click="toggleLike"
          >
            <AppIcon name="heart" :size="18" :filled="liked" />
            <span>{{ likeCount }}</span>
            <span class="sr-only">{{ liked ? 'Unlike' : 'Like' }}</span>
          </button>

          <span class="engagement-metric">
            <AppIcon name="reply" :size="18" />
            <span>{{ commentCount }}</span>
            <span class="sr-only">Replies</span>
          </span>

          <span
            class="engagement-metric"
            :aria-label="detailViewLabel"
            :title="detailViewLabel"
          >
            <AppIcon name="analytics" :size="18" />
            <span>{{ compactViewCount }}</span>
            <span class="sr-only">{{ detailViewLabel }}</span>
          </span>
        </div>

        <p v-if="likeError" class="detail-inline-error" role="status">{{ likeError }}</p>
        <p v-if="deleteError" class="detail-inline-error" role="alert">{{ deleteError }}</p>
      </article>

      <section class="replies-section" aria-labelledby="replies-heading">
        <div class="replies-section__heading">
          <h2 id="replies-heading">Replies</h2>
          <span>{{ commentCount }}</span>
        </div>

        <CommentComposer
          v-if="authStore.isAuthenticated"
          :key="articleId"
          ref="composerRef"
          :author="currentIdentity"
          :submitting="commentSubmitting"
          @submit="handleCreateComment"
        />
        <div v-else class="login-prompt">
          <span>Log in to join the conversation.</span>
          <RouterLink :to="{ name: 'Login' }">Log in</RouterLink>
        </div>

        <p v-if="commentError" class="comment-error" role="alert">{{ commentError }}</p>

        <div v-if="commentsInitialLoading" class="comments-state" aria-live="polite">
          Loading replies...
        </div>
        <div v-else-if="commentsError" class="comments-state comments-state--error" role="alert">
          <p>Replies could not be loaded.</p>
          <span>{{ commentsError }}</span>
          <button type="button" @click="retryInitialComments">Retry</button>
        </div>
        <CommentList
          v-else
          :key="articleId"
          :comments="comments"
          :current-identity="currentIdentity"
          :deleting-comment-id="deletingCommentId"
          :has-next="Boolean(nextCursor)"
          :loading-more="commentsLoadingMore"
          :load-more-error="commentsLoadMoreError"
          @load-more="loadMoreComments"
          @retry="retryLoadMoreComments"
          @delete="deleteOwnComment"
        />
      </section>
    </template>

    <section v-else class="detail-state detail-state--error">
      <h1>{{ articleFailureTitle }}</h1>
      <p>{{ articleFailureMessage }}</p>
      <RouterLink v-if="!authStore.isAuthenticated" class="detail-state__link" :to="{ name: 'Login' }">
        Log in
      </RouterLink>
      <button v-else type="button" @click="retryArticle">Try again</button>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router';
import AuthorIdentity from '../components/AuthorIdentity.vue';
import AppIcon from '../components/icons/AppIcon.vue';
import CommentComposer from '../components/comments/CommentComposer.vue';
import CommentList from '../components/comments/CommentList.vue';
import { createArticleComment, deleteComment, getArticleComments } from '../services/commentService';
import { deleteArticle, getArticleById } from '../services/articleService';
import { getArticleLikeState, likeArticle, unlikeArticle } from '../services/likeService';
import { consumePendingRecommendationAttribution } from '../services/recommendationAttribution';
import { getRecommendationTelemetry } from '../services/recommendationTelemetry';
import { createArticleViewEventID, getArticleViewTelemetry } from '../services/articleViewTelemetry';
import { ArticleReadTracker, createArticleReadGeometry } from '../services/articleReadTracker';
import { useAuthStore } from '../store/auth';
import { useFeedStore } from '../store/feed';
import type { Article } from '../types/Article';
import type { ArticleComment } from '../types/Comment';
import type { RecommendationTracking } from '../types/Recommendation';
import { formatAccessibleEngagementCount, formatCompactEngagementCount } from '../utils/engagementCount';

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const feedStore = useFeedStore();
const currentIdentity = computed(() => authStore.currentIdentity);
const recommendationTelemetry = getRecommendationTelemetry(() => authStore.token);

const articleId = computed(() => String(route.params.id ?? '').trim());
const article = ref<Article | null>(null);
const articleLoading = ref(false);
const articleError = ref('');
const showCover = ref(false);
const deletePending = ref(false);
const deleteError = ref('');
const articleBodyRef = ref<HTMLElement | null>(null);

const liked = ref(false);
const likeCount = ref(0);
const likeStateLoading = ref(false);
const likeSubmitting = ref(false);
const likeError = ref('');

const comments = ref<ArticleComment[]>([]);
const nextCursor = ref<string | null>(null);
const commentsInitialLoading = ref(false);
const commentsLoadingMore = ref(false);
const commentsError = ref('');
const commentsLoadMoreError = ref('');
const commentSubmitting = ref(false);
const commentError = ref('');
const deletingCommentId = ref<number | null>(null);
const composerRef = ref<InstanceType<typeof CommentComposer> | null>(null);
const commentCount = ref(0);
const viewCount = ref(0);

let detailRequestVersion = 0;
let deleteRequestVersion = 0;
let likeRequestVersion = 0;
let likeMutationVersion = 0;
let commentsRequestVersion = 0;
let replyIntentTask: Promise<void> | null = null;
let replyIntentRetryRequested = false;

let tracking: RecommendationTracking | null = null;
let trackedArticleID = '';
let readEndSent = false;
let readTracker: ArticleReadTracker | null = null;
let readResizeObserver: ResizeObserver | null = null;
const clampCount = (value: unknown) => {
  const count = Number(value);
  return Number.isFinite(count) ? Math.max(0, Math.floor(count)) : 0;
};

const isValidArticleID = (value: string) => {
  const parsed = Number(value);
  return /^\d+$/.test(value) && Number.isSafeInteger(parsed) && parsed > 0;
};

const formatArticleDate = (value: string) => {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleDateString();
};

const articleExpiredLabel = computed(() => {
  if (!article.value?.expired_at) {
    return '';
  }

  const date = new Date(article.value.expired_at);
  if (Number.isNaN(date.getTime())) {
    return '';
  }

  return date.getTime() <= Date.now()
    ? 'Expired ' + date.toLocaleString()
    : 'Expires ' + date.toLocaleString();
});

const articleFailureTitle = computed(() => {
  if (!authStore.isAuthenticated) {
    return 'Log in to read this article';
  }
  return 'Article unavailable';
});

const articleFailureMessage = computed(() => {
  if (!authStore.isAuthenticated) {
    return 'Sign in to open the full article and join the conversation.';
  }
  return articleError.value || 'This article could not be found.';
});

const currentViewerID = computed(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isFinite(id) && id > 0 ? id : null;
});
const articleViewTelemetry = getArticleViewTelemetry();

const compactViewCount = computed(() => formatCompactEngagementCount(viewCount.value));
const detailViewLabel = computed(() => formatAccessibleEngagementCount(viewCount.value, 'views'));

const canDeleteArticle = computed(() => Boolean(
  article.value
  && authStore.isAuthenticated
  && currentViewerID.value !== null
  && article.value.author.id === currentViewerID.value,
));

const mergeComments = (items: ArticleComment[]) => {
  const seen = new Set<number>();
  return items.filter(comment => {
    if (seen.has(comment.id)) {
      return false;
    }
    seen.add(comment.id);
    return true;
  });
};

const disconnectReadGeometryObserver = () => {
  readResizeObserver?.disconnect();
  readResizeObserver = null;
};

const getCurrentArticleReadGeometry = () => {
  const element = articleBodyRef.value;
  if (!element) {
    return null;
  }

  const rect = element.getBoundingClientRect();
  return {
    articleTopDoc: window.scrollY + rect.top,
    articleHeight: Math.max(rect.height, 1),
    currentViewportBottomDoc: window.scrollY + window.innerHeight,
  };
};

const updateReadGeometry = () => {
  const geometry = getCurrentArticleReadGeometry();
  if (geometry) {
    readTracker?.updateGeometry(geometry);
  }
};

const handleReadScroll = () => {
  if (readTracker) {
    readTracker.recordScroll(window.scrollY + window.innerHeight);
  }
};

const finishRead = (exitType: string) => {
  if (!tracking || readEndSent || !trackedArticleID || !readTracker) {
    return false;
  }

  const payload = readTracker.finish(exitType);
  if (!payload) {
    return false;
  }

  readEndSent = true;
  return recommendationTelemetry.recordReadEnd(Number(trackedArticleID), tracking, payload);
};

const handleVisibilityChange = () => {
  if (document.visibilityState === 'hidden') {
    readTracker?.pause();
    void recommendationTelemetry.flush(true);
  } else if (tracking && !readEndSent) {
    readTracker?.resume();
  }
};

const handlePageHide = () => {
  finishRead('page_hide');
  void recommendationTelemetry.flush(true);
};

const startRead = (id: string, detailVersion: number) => {
  disconnectReadGeometryObserver();
  readTracker = null;
  tracking = null;
  trackedArticleID = id;
  readEndSent = false;

  if (
    detailVersion !== detailRequestVersion
    || articleId.value !== id
    || article.value?.ID !== Number(id)
  ) {
    return;
  }

  const element = articleBodyRef.value;
  if (!element) {
    return;
  }

  tracking = consumePendingRecommendationAttribution(Number(id));
  if (!tracking) {
    return;
  }

  const rect = element.getBoundingClientRect();
  readTracker = new ArticleReadTracker();
  readTracker.start(
    createArticleReadGeometry({ top: rect.top, height: rect.height }, window.scrollY, window.innerHeight),
    document.visibilityState === 'visible',
  );

  if (typeof ResizeObserver !== 'undefined') {
    readResizeObserver = new ResizeObserver(updateReadGeometry);
    readResizeObserver.observe(element);
  }
};

const resetLikeState = () => {
  likeRequestVersion += 1;
  likeMutationVersion += 1;
  liked.value = false;
  likeCount.value = 0;
  likeStateLoading.value = false;
  likeSubmitting.value = false;
  likeError.value = '';
};

const resetCommentsState = () => {
  commentsRequestVersion += 1;
  comments.value = [];
  nextCursor.value = null;
  commentsInitialLoading.value = false;
  commentsLoadingMore.value = false;
  commentsError.value = '';
  commentsLoadMoreError.value = '';
  commentSubmitting.value = false;
  commentError.value = '';
  deletingCommentId.value = null;
  commentCount.value = 0;
};

const resetArticleState = () => {
  deleteRequestVersion += 1;
  article.value = null;
  articleLoading.value = false;
  articleError.value = '';
  showCover.value = false;
  viewCount.value = 0;
  deletePending.value = false;
  deleteError.value = '';
};

const getErrorStatus = (error: unknown) =>
  (error as { response?: { status?: number } }).response?.status;

const handleDeleteArticle = async () => {
  const currentArticle = article.value;
  const viewerID = currentViewerID.value;
  if (
    !currentArticle
    || viewerID === null
    || !canDeleteArticle.value
    || deletePending.value
  ) {
    return;
  }
  if (!window.confirm('Delete this post? This cannot be undone.')) {
    return;
  }

  const detailVersion = detailRequestVersion;
  const requestVersion = ++deleteRequestVersion;
  const articleID = currentArticle.ID;
  deletePending.value = true;
  deleteError.value = '';

  const isCurrentDelete = () =>
    requestVersion === deleteRequestVersion
    && detailVersion === detailRequestVersion
    && authStore.isAuthenticated
    && currentViewerID.value === viewerID
    && feedStore.viewerID === viewerID
    && article.value?.ID === articleID;

  const finishTerminalDelete = () => {
    if (!isCurrentDelete() || !feedStore.markArticleDeleted(articleID, viewerID)) {
      return false;
    }
    finishRead('route_leave');
    void recommendationTelemetry.flush(false);
    deletePending.value = false;
    deleteError.value = '';
    void router.replace({
      name: 'UserProfile',
      params: { id: String(viewerID) },
    });
    return true;
  };

  try {
    await deleteArticle(articleID);
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
    deleteError.value = status === 403
      ? 'You can only delete your own posts.'
      : status === 401
        ? 'Please log in again to delete this post.'
        : 'Could not delete post. Please try again.';
    deletePending.value = false;
  } finally {
    if (requestVersion === deleteRequestVersion && detailVersion === detailRequestVersion) {
      deletePending.value = false;
    }
  }
};

const loadLikeState = async (id: string, detailVersion: number) => {
  if (!authStore.isAuthenticated) {
    return;
  }

  const requestVersion = ++likeRequestVersion;
  const mutationVersionAtStart = likeMutationVersion;
  likeStateLoading.value = true;
  likeError.value = '';

  try {
    const response = await getArticleLikeState(id);
    if (
      detailVersion !== detailRequestVersion ||
      requestVersion !== likeRequestVersion ||
      mutationVersionAtStart !== likeMutationVersion
    ) {
      return;
    }

    liked.value = response.liked;
    likeCount.value = clampCount(response.likes);
  } catch {
    if (detailVersion === detailRequestVersion && requestVersion === likeRequestVersion) {
      likeError.value = 'Like status is unavailable. You can still try again.';
    }
  } finally {
    if (detailVersion === detailRequestVersion && requestVersion === likeRequestVersion) {
      likeStateLoading.value = false;
    }
  }
};

const toggleLike = async () => {
  if (
    !article.value ||
    !authStore.isAuthenticated ||
    likeStateLoading.value ||
    likeSubmitting.value
  ) {
    return;
  }

  const detailVersion = detailRequestVersion;
  const id = articleId.value;
  const mutationVersion = ++likeMutationVersion;
  const previousLiked = liked.value;
  const previousCount = likeCount.value;

  likeSubmitting.value = true;
  likeError.value = '';
  liked.value = !previousLiked;
  likeCount.value = Math.max(0, previousCount + (liked.value ? 1 : -1));

  try {
    const response = previousLiked ? await unlikeArticle(id) : await likeArticle(id);
    if (detailVersion !== detailRequestVersion || mutationVersion !== likeMutationVersion) {
      return;
    }

    liked.value = response.liked;
    likeCount.value = clampCount(response.likes);
  } catch {
    if (detailVersion === detailRequestVersion && mutationVersion === likeMutationVersion) {
      liked.value = previousLiked;
      likeCount.value = Math.max(0, previousCount);
      likeError.value = 'Like failed. Please try again.';
    }
  } finally {
    if (detailVersion === detailRequestVersion && mutationVersion === likeMutationVersion) {
      likeSubmitting.value = false;
    }
  }
};

const loadInitialComments = async (id: string, detailVersion: number) => {
  const requestVersion = ++commentsRequestVersion;
  commentsInitialLoading.value = true;
  commentsLoadingMore.value = false;
  commentsError.value = '';
  commentsLoadMoreError.value = '';
  comments.value = [];
  nextCursor.value = null;

  try {
    const page = await getArticleComments(id, { limit: 20 });
    if (detailVersion !== detailRequestVersion || requestVersion !== commentsRequestVersion) {
      return;
    }

    comments.value = mergeComments(comments.value.concat(page.items));
    nextCursor.value = page.next_cursor || null;
  } catch {
    if (detailVersion === detailRequestVersion && requestVersion === commentsRequestVersion) {
      commentsError.value = 'The replies could not be loaded.';
    }
  } finally {
    if (detailVersion === detailRequestVersion && requestVersion === commentsRequestVersion) {
      commentsInitialLoading.value = false;
    }
  }
};

const loadMoreComments = async () => {
  if (
    !nextCursor.value ||
    commentsInitialLoading.value ||
    commentsLoadingMore.value ||
    !article.value
  ) {
    return;
  }

  const id = articleId.value;
  const detailVersion = detailRequestVersion;
  const requestVersion = ++commentsRequestVersion;
  const cursor = nextCursor.value;
  commentsLoadingMore.value = true;
  commentsLoadMoreError.value = '';

  try {
    const page = await getArticleComments(id, { limit: 20, cursor });
    if (detailVersion !== detailRequestVersion || requestVersion !== commentsRequestVersion) {
      return;
    }

    comments.value = mergeComments(comments.value.concat(page.items));
    nextCursor.value = page.next_cursor || null;
  } catch {
    if (detailVersion === detailRequestVersion && requestVersion === commentsRequestVersion) {
      commentsLoadMoreError.value = 'Could not load more replies.';
    }
  } finally {
    if (detailVersion === detailRequestVersion && requestVersion === commentsRequestVersion) {
      commentsLoadingMore.value = false;
    }
  }
};

const retryInitialComments = () => {
  if (article.value) {
    void loadInitialComments(articleId.value, detailRequestVersion);
  }
};

const retryLoadMoreComments = () => {
  void loadMoreComments();
};

const handleCreateComment = async (content: string) => {
  if (!article.value || !authStore.isAuthenticated || commentSubmitting.value) {
    return;
  }

  const id = articleId.value;
  const detailVersion = detailRequestVersion;
  commentSubmitting.value = true;
  commentError.value = '';

  try {
    const created = await createArticleComment(id, content);
    if (detailVersion !== detailRequestVersion || articleId.value !== id) {
      return;
    }

    comments.value = mergeComments([created].concat(comments.value));
    commentsError.value = '';
    commentCount.value = clampCount(commentCount.value + 1);
    composerRef.value?.clear();
  } catch {
    if (detailVersion === detailRequestVersion) {
      commentError.value = 'Reply failed. Please try again.';
    }
  } finally {
    if (detailVersion === detailRequestVersion) {
      commentSubmitting.value = false;
    }
  }
};

const deleteOwnComment = async (commentID: number) => {
  if (
    !authStore.isAuthenticated ||
    deletingCommentId.value !== null ||
    !comments.value.some(comment => comment.id === commentID)
  ) {
    return;
  }

  const detailVersion = detailRequestVersion;
  deletingCommentId.value = commentID;
  commentError.value = '';

  try {
    await deleteComment(commentID);
    if (detailVersion !== detailRequestVersion) {
      return;
    }

    comments.value = comments.value.filter(comment => comment.id !== commentID);
    commentCount.value = Math.max(0, commentCount.value - 1);
  } catch {
    if (detailVersion === detailRequestVersion) {
      commentError.value = 'Reply could not be deleted. Please try again.';
    }
  } finally {
    if (detailVersion === detailRequestVersion && deletingCommentId.value === commentID) {
      deletingCommentId.value = null;
    }
  }
};

const consumeReplyIntent = async () => {
  if (replyIntentTask) {
    replyIntentRetryRequested = true;
    return;
  }

  const task = (async () => {
    if (route.query.reply !== '1' || !authStore.isAuthenticated || !article.value) {
      return;
    }

    const detailVersion = detailRequestVersion;
    const id = articleId.value;
    await nextTick();

    const isCurrentIntent = () =>
      route.query.reply === '1'
      && authStore.isAuthenticated
      && detailVersion === detailRequestVersion
      && id === articleId.value
      && Boolean(article.value);

    if (!isCurrentIntent()) {
      return;
    }

    const composer = composerRef.value;
    if (!composer || !await composer.focus()) {
      return;
    }

    if (!isCurrentIntent()) {
      return;
    }

    const query = { ...route.query };
    delete query.reply;

    try {
      await router.replace({
        name: 'NewsDetail',
        params: route.params,
        query,
        hash: route.hash,
      });
    } catch {
      // Keep the one-shot intent in the URL if the replacement is rejected.
    }
  })();

  replyIntentTask = task;
  try {
    await task;
  } finally {
    if (replyIntentTask !== task) {
      return;
    }

    replyIntentTask = null;
    const shouldRetry = replyIntentRetryRequested;
    replyIntentRetryRequested = false;
    if (shouldRetry && route.query.reply === '1') {
      void consumeReplyIntent();
    }
  }
};

const loadDetail = async (id: string, isAuthenticated: boolean) => {
  const detailVersion = ++detailRequestVersion;
  finishRead('navigate_to_article');
  void recommendationTelemetry.flush(false);
  resetArticleState();
  resetLikeState();
  resetCommentsState();

  if (!isAuthenticated) {
    return;
  }

  if (!isValidArticleID(id)) {
    articleError.value = 'This article URL is not valid.';
    return;
  }

  articleLoading.value = true;

  try {
    const loadedArticle = await getArticleById(id);
    if (detailVersion !== detailRequestVersion) {
      return;
    }
    if (loadedArticle.ID !== Number(id)) {
      throw new Error('article response id mismatch');
    }

    article.value = loadedArticle;
    showCover.value = Boolean(loadedArticle.cover_image_url);
    likeCount.value = clampCount(loadedArticle.like_count);
    commentCount.value = clampCount(loadedArticle.comment_count);
    viewCount.value = clampCount(loadedArticle.view_count);
    articleLoading.value = false;
    await nextTick();
    if (
      detailVersion !== detailRequestVersion
      || articleId.value !== id
      || article.value?.ID !== Number(id)
    ) {
      return;
    }

    articleViewTelemetry.enqueue(Number(id), createArticleViewEventID(), 'article_detail');
    if (articleBodyRef.value) {
      startRead(id, detailVersion);
    }
    void loadLikeState(id, detailVersion);
    void loadInitialComments(id, detailVersion);
  } catch (error) {
    if (detailVersion === detailRequestVersion) {
      const status = (error as { response?: { status?: number } }).response?.status;
      articleError.value = status === 404
        ? 'This article does not exist.'
        : 'The article could not be loaded.';
    }
  } finally {
    if (detailVersion === detailRequestVersion) {
      articleLoading.value = false;
    }
  }
};

const retryArticle = () => {
  void loadDetail(articleId.value, authStore.isAuthenticated);
};

const goBack = () => {
  const historyState = window.history.state as { back?: string | null } | null;
  if (historyState?.back) {
    router.back();
    return;
  }

  void router.push({ name: 'Home' });
};

const hideCover = () => {
  showCover.value = false;
};

watch(
  [articleId, () => authStore.isAuthenticated],
  ([id, isAuthenticated]) => {
    void loadDetail(id, isAuthenticated);
  },
  { immediate: true },
);

watch(currentViewerID, (viewerID, previousViewerID) => {
  if (viewerID === previousViewerID) {
    return;
  }
  deleteRequestVersion += 1;
  deletePending.value = false;
  deleteError.value = '';
});

watch(
  [() => route.query.reply, article, () => authStore.isAuthenticated, commentSubmitting],
  () => {
    if (route.query.reply === '1') {
      void consumeReplyIntent();
    }
  },
  { flush: 'post' },
);

onBeforeRouteLeave(to => {
  finishRead(to.name === 'Recommendations' ? 'back_to_recommendation' : 'route_leave');
  void recommendationTelemetry.flush(false);
});

onMounted(() => {
  document.addEventListener('visibilitychange', handleVisibilityChange);
  window.addEventListener('scroll', handleReadScroll, { passive: true });
  window.addEventListener('resize', updateReadGeometry);
  window.addEventListener('pagehide', handlePageHide);
});

onBeforeUnmount(() => {
  finishRead('route_leave');
  void recommendationTelemetry.flush(false);
  disconnectReadGeometryObserver();
  document.removeEventListener('visibilitychange', handleVisibilityChange);
  window.removeEventListener('scroll', handleReadScroll);
  window.removeEventListener('resize', updateReadGeometry);
  window.removeEventListener('pagehide', handlePageHide);
});
</script>

<style scoped>
.detail-view {
  min-height: 100vh;
  background: var(--color-surface);
  color: var(--color-text);
}

.detail-header {
  position: sticky;
  top: 0;
  z-index: 12;
  display: flex;
  align-items: center;
  min-height: 56px;
  padding: 0 var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.detail-header__back {
  display: inline-flex;
  align-items: center;
  gap: var(--space-3);
  min-height: 40px;
  border: 0;
  padding: 0;
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
  font-size: 15px;
  font-weight: 750;
}

.detail-header__back:hover {
  color: var(--color-accent);
}

.detail-header__back .app-icon {
  flex: 0 0 auto;
}

.article-detail,
.replies-section {
  padding: var(--space-5);
}

.article-detail {
  border-bottom: 1px solid var(--color-border);
}

.article-detail > .author-identity {
  margin-bottom: var(--space-5);
}

.article-detail__title {
  margin: 0;
  color: var(--color-text);
  font-size: clamp(28px, 4vw, 42px);
  line-height: 1.1;
  letter-spacing: -0.04em;
  overflow-wrap: anywhere;
}

.article-detail__expiry {
  display: inline-block;
  margin: var(--space-4) 0 0;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.article-detail__body {
  margin-top: var(--space-5);
  color: var(--color-text);
  font-size: 16px;
  line-height: 1.75;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.article-detail__cover {
  margin: var(--space-5) 0 0;
  overflow: hidden;
  border-radius: var(--radius-sm);
  background: var(--color-surface-subtle);
}

.article-detail__cover img {
  display: block;
  width: 100%;
  max-height: 620px;
  object-fit: cover;
}

.article-detail__meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
  margin-top: var(--space-5);
  color: var(--color-text-tertiary);
  font-size: 12px;
}

.detail-delete-action {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  margin-left: auto;
  border: 0;
  padding: 0;
  background: transparent;
  color: var(--color-danger);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
}

.detail-delete-action:hover,
.detail-delete-action:focus-visible {
  text-decoration: underline;
}

.detail-delete-action:disabled {
  cursor: wait;
  opacity: 0.64;
}

.article-detail__engagement {
  display: flex;
  align-items: center;
  gap: var(--space-5);
  margin-top: var(--space-4);
  padding-top: var(--space-4);
  border-top: 1px solid var(--color-border);
}

.engagement-action,
.engagement-metric {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 34px;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.engagement-action {
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-2);
  background: transparent;
  cursor: pointer;
}

.engagement-action:hover:not(:disabled),
.engagement-action--liked {
  background: color-mix(in srgb, var(--color-accent) 10%, transparent);
  color: var(--color-accent);
}

.engagement-action:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.engagement-action .app-icon,
.engagement-metric .app-icon {
  flex: 0 0 auto;
}

.detail-inline-error,
.comment-error {
  margin: var(--space-3) 0 0;
  color: var(--color-danger);
  font-size: 12px;
}

.replies-section {
  padding-top: var(--space-4);
}

.replies-section__heading {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  padding-bottom: var(--space-2);
}

.replies-section__heading h2 {
  margin: 0;
  font-size: 20px;
  letter-spacing: -0.02em;
}

.replies-section__heading span {
  color: var(--color-text-tertiary);
  font-size: 13px;
}

.login-prompt {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-4) 0;
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-secondary);
  font-size: 13px;
}

.login-prompt a,
.detail-state__link {
  color: var(--color-accent);
  font-weight: 750;
  text-decoration: none;
}

.login-prompt a:hover,
.detail-state__link:hover {
  text-decoration: underline;
}

.comments-state {
  padding: var(--space-8) 0;
  color: var(--color-text-secondary);
  text-align: center;
}

.comments-state--error {
  display: grid;
  justify-items: center;
  gap: var(--space-2);
}

.comments-state p {
  margin: 0;
  color: var(--color-text);
  font-weight: 700;
}

.comments-state span {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.comments-state button,
.detail-state button {
  min-height: 34px;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-4);
  background: var(--color-accent);
  color: #fff;
  cursor: pointer;
  font-size: 13px;
  font-weight: 750;
}

.detail-state {
  display: grid;
  justify-items: center;
  gap: var(--space-3);
  min-height: 360px;
  padding: var(--space-8) var(--space-5);
  align-content: center;
  color: var(--color-text-secondary);
  text-align: center;
}

.detail-state p,
.detail-state h1 {
  margin: 0;
}

.detail-state h1 {
  color: var(--color-text);
  font-size: 24px;
}

.detail-state--error {
  border-bottom: 1px solid var(--color-border);
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  clip-path: inset(50%);
}

@media (max-width: 799px) {
  .detail-header {
    top: var(--app-mobile-nav-offset, 0px);
  }
}

@media (max-width: 420px) {
  .detail-header {
    padding-inline: var(--space-4);
  }

  .article-detail,
  .replies-section {
    padding-inline: var(--space-4);
  }

  .article-detail__title {
    font-size: 28px;
  }

  .login-prompt {
    align-items: flex-start;
    flex-direction: column;
  }
}

@media (prefers-reduced-motion: reduce) {
  .detail-header {
    backdrop-filter: none;
  }
}

</style>
