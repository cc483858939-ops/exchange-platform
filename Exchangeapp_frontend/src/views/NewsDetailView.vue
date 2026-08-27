<template>
  <main class="detail-view">
    <header class="detail-header">
      <button class="detail-header__back" type="button" aria-label="Back" @click="goBack">
        <AppIcon name="arrow-left" :size="20" />
      </button>
      <h1 class="detail-header__title">Post</h1>
    </header>

    <template v-if="detailPresentation">
      <article class="post-detail">
        <div class="post-detail__author-row">
          <AuthorIdentity
            :author="detailPresentation.author"
            variant="post"
          />
          <button
            v-if="detailPresentation.kind === 'article' && canDeleteArticle"
            class="post-detail__delete"
            type="button"
            aria-label="Delete post"
            title="Delete post"
            :disabled="deletePending"
            :aria-busy="deletePending"
            @click="handleDeleteArticle"
          >
            <AppIcon name="trash" :size="18" />
          </button>
        </div>

        <p v-if="detailPresentation.title.trim()" class="post-detail__headline">
          {{ detailPresentation.title }}
        </p>

        <div
          ref="articleBodyRef"
          class="post-detail__body"
          :class="{ 'post-detail__body--loading': detailPresentation.kind === 'warm' }"
          :aria-busy="detailPresentation.kind === 'warm' ? 'true' : undefined"
        >{{ detailPresentation.body }}</div>

        <figure v-if="detailPresentation.coverUrl" class="post-detail__cover">
          <img
            v-if="failedCoverUrl !== detailPresentation.coverUrl"
            :src="detailPresentation.coverUrl"
            :alt="detailPresentation.title.trim() || 'Post image'"
            loading="lazy"
            @error="handleCoverError"
          />
          <div
            v-else
            class="post-detail__cover-placeholder"
            role="img"
            aria-label="Post image unavailable"
          ></div>
        </figure>

        <div class="post-detail__meta">
          <span v-if="detailPostTimestamp">{{ detailPostTimestamp }}</span>
          <span
            class="post-detail__views"
            :aria-label="postViewsLabel"
            :title="postViewsLabel"
          >
            {{ formattedViews }} Views
          </span>
        </div>

        <div class="post-detail__engagement" aria-label="Post engagement">
          <template v-if="detailPresentation.kind === 'article'">
            <button
              class="post-detail__metric post-detail__reply"
              type="button"
              :aria-label="detailReplyLabel"
              @click="focusReplyComposer"
            >
              <AppIcon name="reply" :size="18" />
              <span>{{ commentCount }}</span>
            </button>

            <RepostAction
              :key="articleId"
              :reposted="reposted"
              :count="repostCount"
              :disabled="!authStore.isAuthenticated || repostStateUnavailable"
              :loading="repostStateLoading"
              :pending="repostSubmitting"
              :ariaLabel="detailRepostLabel"
              variant="detail"
              @toggle="toggleRepost"
            />
            <LikeAction
              :key="articleId"
              :liked="liked"
              :count="likeCount"
              :disabled="!authStore.isAuthenticated"
              :loading="likeStateLoading"
              :pending="likeSubmitting"
              :ariaLabel="detailLikeLabel"
              variant="detail"
              @toggle="toggleLike"
            />
          </template>
          <template v-else>
            <span
              class="post-detail__metric post-detail__reply"
              :aria-label="presentationCommentLabel"
              :title="presentationCommentLabel"
            >
              <AppIcon name="reply" :size="18" />
              <span>{{ detailPresentation.commentCount }}</span>
            </span>
            <span
              class="post-detail__metric post-detail__repost"
              :aria-label="presentationRepostLabel"
              :title="presentationRepostLabel"
            >
              <AppIcon name="repost" :size="18" />
              <span>{{ detailPresentation.repostCount }}</span>
            </span>
            <span
              class="post-detail__metric post-detail__like"
              :aria-label="presentationLikeLabel"
              :title="presentationLikeLabel"
            >
              <AppIcon name="heart" :size="18" />
              <span>{{ detailPresentation.likeCount }}</span>
            </span>
          </template>
        </div>

        <p
          v-if="detailPresentation.kind === 'article' && likeError"
          class="detail-inline-error"
          role="status"
        >{{ likeError }}</p>
        <p
          v-if="detailPresentation.kind === 'article' && repostError"
          class="detail-inline-error"
          role="status"
        >{{ repostError }}</p>
        <p
          v-if="detailPresentation.kind === 'article' && deleteError"
          class="detail-inline-error"
          role="alert"
        >{{ deleteError }}</p>

        <div
          v-if="detailPresentation.kind === 'warm'"
          class="detail-warm-loading"
          role="status"
          aria-live="polite"
        >
          <span class="detail-loading__spinner" aria-hidden="true"></span>
          <span class="sr-only">Loading full post</span>
        </div>
      </article>

      <section
        v-if="detailPresentation.kind === 'article'"
        class="post-conversation"
        aria-label="Conversation"
      >
        <CommentComposer
          :key="articleId"
          ref="composerRef"
          :author="replyComposerAuthor"
          v-model="replyDraftContent"
          :submitting="commentSubmitting"
          @submit="handleCreateComment"
        />

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

    <section v-else-if="articleLoading" class="detail-loading" role="status" aria-live="polite">
      <span class="detail-loading__spinner" aria-hidden="true"></span>
      <span class="sr-only">Loading post</span>
    </section>

    <section v-else class="detail-state detail-state--error">
      <h2>{{ articleFailureTitle }}</h2>
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
import LikeAction from '../components/engagement/LikeAction.vue';
import RepostAction from '../components/engagement/RepostAction.vue';
import AppIcon from '../components/icons/AppIcon.vue';
import CommentComposer from '../components/comments/CommentComposer.vue';
import CommentList from '../components/comments/CommentList.vue';
import { createArticleComment, deleteComment, getArticleComments } from '../services/commentService';
import { deleteArticle, getArticleById } from '../services/articleService';
import { getArticleLikeState, likeArticle, unlikeArticle } from '../services/likeService';
import { getArticleRepostState, repostArticle, undoRepostArticle } from '../services/repostService';
import { consumePendingRecommendationAttribution } from '../services/recommendationAttribution';
import { getRecommendationTelemetry } from '../services/recommendationTelemetry';
import { createArticleViewEventID, getArticleViewTelemetry } from '../services/articleViewTelemetry';
import { ArticleReadTracker, createArticleReadGeometry } from '../services/articleReadTracker';
import { useAuthStore } from '../store/auth';
import { useArticleDetailHandoffStore } from '../store/articleDetailHandoff';
import { useFeedStore } from '../store/feed';
import { useReplyDraftStore } from '../store/replyDraft';
import {
  syncExternalArticleLikeState,
  syncExternalArticleRepostState,
  syncExternalArticleRemoval,
  syncExternalCommentCount,
} from '../store/sessionSync';
import type { Article } from '../types/Article';
import type { ArticleComment } from '../types/Comment';
import type { FeedPost } from '../types/Feed';
import type { RecommendationTracking } from '../types/Recommendation';
import type { PublicAuthor } from '../types/User';
import { formatAccessibleEngagementCount, formatCompactEngagementCount } from '../utils/engagementCount';
import { formatPostDetailTimestamp } from '../utils/time';

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const articleDetailHandoff = useArticleDetailHandoffStore();
const feedStore = useFeedStore();
const replyDraftStore = useReplyDraftStore();
const currentIdentity = computed(() => authStore.currentIdentity);
const recommendationTelemetry = getRecommendationTelemetry(() => authStore.token);

const articleId = computed(() => String(route.params.id ?? '').trim());
const article = ref<Article | null>(null);
const articleLoading = ref(false);
const articleError = ref('');
const handoffPost = ref<FeedPost | null>(null);
const failedCoverUrl = ref('');
const deletePending = ref(false);
const deleteError = ref('');
const articleBodyRef = ref<HTMLElement | null>(null);

const liked = ref(false);
const likeCount = ref(0);
const likeStateLoading = ref(false);
const likeSubmitting = ref(false);
const likeError = ref('');
const reposted = ref(false);
const repostCount = ref(0);
const repostStateLoading = ref(false);
const repostSubmitting = ref(false);
const repostError = ref('');
const repostStateUnavailable = ref(false);

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
let repostRequestVersion = 0;
let repostMutationVersion = 0;
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

type DetailPresentation = {
  kind: 'warm' | 'article';
  author: PublicAuthor;
  title: string;
  body: string;
  createdAt: string;
  coverUrl: string;
  likeCount: number;
  repostCount: number;
  reposted: boolean;
  commentCount: number;
  viewCount: number;
};

const detailPresentation = computed<DetailPresentation | null>(() => {
  if (article.value) {
    return {
      kind: 'article',
      author: article.value.author,
      title: article.value.title,
      body: article.value.content,
      createdAt: article.value.CreatedAt,
      coverUrl: article.value.cover_image_url || '',
      likeCount: likeCount.value,
      repostCount: repostCount.value,
      reposted: reposted.value,
      commentCount: commentCount.value,
      viewCount: viewCount.value,
    };
  }

  if (articleLoading.value && handoffPost.value) {
    return {
      kind: 'warm',
      author: handoffPost.value.author,
      title: handoffPost.value.title,
      body: handoffPost.value.excerpt,
      createdAt: handoffPost.value.createdAt,
      coverUrl: handoffPost.value.coverImageUrl,
      likeCount: handoffPost.value.likeCount,
      repostCount: handoffPost.value.repostCount,
      reposted: handoffPost.value.reposted,
      commentCount: handoffPost.value.commentCount,
      viewCount: handoffPost.value.viewCount,
    };
  }

  return null;
});

const presentationLikeLabel = computed(() => {
  const count = detailPresentation.value?.likeCount ?? 0;
  return String(count) + (count === 1 ? ' like' : ' likes');
});

const presentationCommentLabel = computed(() => {
  const count = detailPresentation.value?.commentCount ?? 0;
  return String(count) + (count === 1 ? ' reply' : ' replies');
});

const presentationRepostLabel = computed(() => {
  const count = detailPresentation.value?.repostCount ?? 0;
  return String(count) + (count === 1 ? ' repost' : ' reposts');
});

const articleFailureTitle = computed(() => {
  if (!authStore.isAuthenticated) {
    return 'Log in to view this post';
  }
  return 'Post unavailable';
});

const articleFailureMessage = computed(() => {
  if (!authStore.isAuthenticated) {
    return 'Sign in to open this post and join the conversation.';
  }
  return articleError.value || 'The post could not be loaded.';
});

const currentViewerID = computed(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isFinite(id) && id > 0 ? id : null;
});
const replyComposerAuthor = computed<PublicAuthor | null>(() => {
  const identity = currentIdentity.value;
  const viewerID = currentViewerID.value;

  if (!identity || viewerID === null) {
    return null;
  }

  return {
    id: viewerID,
    username: identity.username,
    display_name: identity.display_name,
    avatar_url: identity.avatar_url,
  };
});

const replyDraftContent = computed({
  get: () => replyDraftStore.getDraft(Number(articleId.value)),
  set: value => replyDraftStore.setDraft(Number(articleId.value), value),
});

const focusReplyComposer = async () => {
  if (!article.value || !authStore.isAuthenticated) {
    return;
  }

  await composerRef.value?.focus();
};

const articleViewTelemetry = getArticleViewTelemetry();

const detailPostTimestamp = computed(() => (
  formatPostDetailTimestamp(detailPresentation.value?.createdAt)
));
const formattedViews = computed(() => (
  formatCompactEngagementCount(detailPresentation.value?.viewCount ?? 0)
));
const postViewsLabel = computed(() => (
  formatAccessibleEngagementCount(detailPresentation.value?.viewCount ?? 0, 'views')
));
const detailReplyLabel = computed(() => {
  const count = commentCount.value;
  return 'Reply to post, ' + count + (count === 1 ? ' reply' : ' replies');
});
const detailLikeLabel = computed(() => {
  const count = String(likeCount.value)
    + (likeCount.value === 1 ? ' like' : ' likes');
  return liked.value
    ? 'Unlike post, ' + count
    : 'Like post, ' + count;
});
const detailRepostLabel = computed(() => {
  const count = String(repostCount.value)
    + (repostCount.value === 1 ? ' repost' : ' reposts');
  return reposted.value
    ? 'Undo repost, ' + count
    : 'Repost post, ' + count;
});
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

const resetRepostState = () => {
  repostRequestVersion += 1;
  repostMutationVersion += 1;
  reposted.value = false;
  repostCount.value = 0;
  repostStateLoading.value = false;
  repostSubmitting.value = false;
  repostError.value = '';
  repostStateUnavailable.value = false;
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
  failedCoverUrl.value = '';
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
    syncExternalArticleRemoval(articleID);
    replyDraftStore.clearDraft(articleID);
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

const loadRepostState = async (id: string, detailVersion: number) => {
  if (!authStore.isAuthenticated) {
    return;
  }

  const requestVersion = ++repostRequestVersion;
  const mutationVersionAtStart = repostMutationVersion;
  repostStateLoading.value = true;
  repostError.value = '';
  repostStateUnavailable.value = false;

  try {
    const response = await getArticleRepostState(id);
    if (
      detailVersion !== detailRequestVersion
      || requestVersion !== repostRequestVersion
      || mutationVersionAtStart !== repostMutationVersion
    ) {
      return;
    }

    reposted.value = response.reposted;
    repostCount.value = clampCount(response.reposts);
    repostStateUnavailable.value = false;
  } catch {
    if (
      detailVersion === detailRequestVersion
      && requestVersion === repostRequestVersion
      && mutationVersionAtStart === repostMutationVersion
    ) {
      repostError.value = 'Repost status is unavailable. You can still try again.';
      repostStateUnavailable.value = true;
    }
  } finally {
    if (
      detailVersion === detailRequestVersion
      && requestVersion === repostRequestVersion
      && mutationVersionAtStart === repostMutationVersion
    ) {
      repostStateLoading.value = false;
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
    syncExternalArticleLikeState({
      articleId: Number(id),
      likes: likeCount.value,
      liked: response.liked,
      status: 'ready',
    });
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

const toggleRepost = async () => {
  if (
    !article.value
    || !authStore.isAuthenticated
    || repostStateLoading.value
    || repostSubmitting.value
    || repostStateUnavailable.value
  ) {
    return;
  }

  const detailVersion = detailRequestVersion;
  const id = articleId.value;
  const mutationVersion = ++repostMutationVersion;
  const previousReposted = reposted.value;
  const previousCount = repostCount.value;

  repostSubmitting.value = true;
  repostError.value = '';
  repostStateUnavailable.value = false;
  reposted.value = !previousReposted;
  repostCount.value = Math.max(0, previousCount + (reposted.value ? 1 : -1));

  try {
    const response = previousReposted ? await undoRepostArticle(id) : await repostArticle(id);
    if (detailVersion !== detailRequestVersion || mutationVersion !== repostMutationVersion) {
      return;
    }

    reposted.value = response.reposted;
    repostCount.value = clampCount(response.reposts);
    syncExternalArticleRepostState({
      articleId: Number(id),
      reposts: repostCount.value,
      reposted: response.reposted,
      status: 'ready',
    });
  } catch {
    if (detailVersion === detailRequestVersion && mutationVersion === repostMutationVersion) {
      reposted.value = previousReposted;
      repostCount.value = Math.max(0, previousCount);
      repostError.value = 'Could not update repost. Please try again.';
      repostStateUnavailable.value = false;
    }
  } finally {
    if (detailVersion === detailRequestVersion && mutationVersion === repostMutationVersion) {
      repostSubmitting.value = false;
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
  const numericArticleID = Number(id);
  const submittingViewerID = currentViewerID.value;
  const submittedDraftSnapshot = replyDraftStore.getDraft(numericArticleID);
  const detailVersion = detailRequestVersion;
  commentSubmitting.value = true;
  commentError.value = '';

  try {
    const created = await createArticleComment(id, content);
    if (
      replyDraftStore.viewerID === submittingViewerID
      && replyDraftStore.getDraft(numericArticleID) === submittedDraftSnapshot
    ) {
      replyDraftStore.clearDraft(numericArticleID);
    }

    if (detailVersion !== detailRequestVersion || articleId.value !== id) {
      return;
    }

    comments.value = mergeComments([created].concat(comments.value));
    commentsError.value = '';
    commentCount.value = clampCount(commentCount.value + 1);
    syncExternalCommentCount({
      articleId: Number(id),
      commentCount: commentCount.value,
    });
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
    syncExternalCommentCount({
      articleId: Number(articleId.value),
      commentCount: commentCount.value,
    });
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
  resetRepostState();
  resetCommentsState();
  handoffPost.value = null;

  if (!isAuthenticated) {
    return;
  }

  if (!isValidArticleID(id)) {
    articleError.value = 'This post URL is not valid.';
    return;
  }

  handoffPost.value = articleDetailHandoff.consume(Number(id));
  articleLoading.value = true;

  try {
    const loadedArticle = await getArticleById(id);
    if (detailVersion !== detailRequestVersion) {
      return;
    }
    if (loadedArticle.ID !== Number(id)) {
      throw new Error('article response id mismatch');
    }

    handoffPost.value = null;
    article.value = loadedArticle;
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
    void loadRepostState(id, detailVersion);
    void loadInitialComments(id, detailVersion);
  } catch (error) {
    if (detailVersion === detailRequestVersion) {
      handoffPost.value = null;
      const status = (error as { response?: { status?: number } }).response?.status;
      if (status === 404) {
        replyDraftStore.clearDraft(Number(id));
      }
      articleError.value = status === 404
        ? 'This post does not exist.'
        : 'The post could not be loaded.';
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

const handleCoverError = () => {
  const coverUrl = detailPresentation.value?.coverUrl ?? '';
  if (coverUrl) {
    failedCoverUrl.value = coverUrl;
  }
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
  currentViewerID,
  viewerID => {
    replyDraftStore.setViewer(viewerID);
  },
  { immediate: true },
);

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
  display: grid;
  width: 40px;
  height: 40px;
  flex: 0 0 40px;
  place-items: center;
  border: 0;
  border-radius: 50%;
  padding: 0;
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: color var(--transition-fast), background-color var(--transition-fast), transform var(--transition-fast);
}

.detail-header__back:hover,
.detail-header__back:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

.detail-header__back:active {
  transform: scale(0.96);
}

.detail-header__back .app-icon {
  flex: 0 0 auto;
}

.detail-header__title {
  margin: 0 0 0 var(--space-3);
  color: var(--color-text);
  font-size: 20px;
  font-weight: 800;
  letter-spacing: -0.02em;
}

.post-detail,
.post-conversation {
  padding: var(--space-5);
}

.post-detail {
  border-bottom: 1px solid var(--color-border);
}

.post-conversation {
  padding-top: 0;
}

.post-detail__author-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  min-height: 40px;
}

.post-detail__delete {
  display: grid;
  width: 40px;
  height: 40px;
  flex: 0 0 40px;
  margin-left: auto;
  place-items: center;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: color var(--transition-fast), background-color var(--transition-fast), transform var(--transition-fast);
}

.post-detail__delete:hover,
.post-detail__delete:focus-visible {
  background: color-mix(in srgb, var(--color-danger) 10%, transparent);
  color: var(--color-danger);
}

.post-detail__delete:active {
  transform: scale(0.96);
}

.post-detail__delete:disabled {
  cursor: wait;
  opacity: 0.64;
}

.post-detail__headline {
  margin: var(--space-3) 0 0;
  color: var(--color-text);
  font-size: 18px;
  font-weight: 700;
  line-height: 1.4;
  letter-spacing: -0.01em;
  overflow-wrap: anywhere;
}

.post-detail__body {
  margin-top: var(--space-3);
  color: var(--color-text);
  font-size: 16px;
  line-height: 1.55;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.post-detail__body--loading {
  color: var(--color-text-secondary);
}

.post-detail__cover {
  aspect-ratio: 16 / 9;
  margin: var(--space-3) 0 0;
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  background: var(--color-surface-subtle);
}

.post-detail__cover img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.post-detail__cover-placeholder {
  width: 100%;
  height: 100%;
  background: var(--color-surface-subtle);
}

.post-detail__meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-3);
  color: var(--color-text-tertiary);
  font-size: 12px;
  line-height: 1.4;
}

.post-detail__meta > span + span::before {
  content: '·';
  margin-right: var(--space-2);
  color: var(--color-border-strong);
}

.post-detail__views {
  white-space: nowrap;
}

.post-detail__engagement {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin-top: var(--space-3);
}

.post-detail__metric {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  min-width: 40px;
  min-height: 40px;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-3);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.post-detail__metric .app-icon {
  flex: 0 0 auto;
}

.post-detail__engagement > button.post-detail__metric {
  cursor: pointer;
  transition: color var(--transition-fast), background-color var(--transition-fast), transform var(--transition-fast);
}

.post-detail__engagement > button.post-detail__metric:hover,
.post-detail__engagement > button.post-detail__metric:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

.post-detail__engagement > button.post-detail__metric:active {
  transform: scale(0.97);
}

.detail-inline-error,
.comment-error {
  margin: var(--space-3) 0 0;
  color: var(--color-danger);
  font-size: 12px;
}

.detail-state__link {
  color: var(--color-accent);
  font-weight: 750;
  text-decoration: none;
}

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

.detail-loading {
  display: grid;
  min-height: 220px;
  place-items: center;
  padding: var(--space-8) var(--space-5);
}

.detail-warm-loading {
  display: grid;
  min-height: 52px;
  place-items: center;
  margin-top: var(--space-4);
}

.detail-loading__spinner {
  display: block;
  width: 24px;
  height: 24px;
  border: 2px solid var(--color-border-strong);
  border-top-color: var(--color-accent);
  border-radius: 50%;
  animation: detail-spin 700ms linear infinite;
}

.detail-warm-loading .detail-loading__spinner {
  width: 18px;
  height: 18px;
}

@keyframes detail-spin {
  to {
    transform: rotate(360deg);
  }
}

.detail-state p,
.detail-state h2 {
  margin: 0;
}

.detail-state h2 {
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
    top: var(--mobile-safe-top);
  }
}

@media (max-width: 420px) {
  .detail-header {
    padding-inline: var(--space-4);
  }

  .post-detail,
  .post-conversation {
    padding-inline: var(--space-4);
  }
}

@media (prefers-reduced-motion: reduce) {
  .detail-header {
    backdrop-filter: none;
  }

  .detail-loading__spinner {
    animation-duration: 1.4s;
  }
}

</style>
