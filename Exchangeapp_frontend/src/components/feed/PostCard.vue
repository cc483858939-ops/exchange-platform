<template>
  <article ref="postCardRef" class="post-card">
    <div class="post-card__header">
      <div class="post-card__author-stack">
        <div v-if="post.repostContext" class="post-card__repost-context">
          <AppIcon name="repost" :size="14" />
          <span>{{ repostContextLabel }}</span>
        </div>
        <AuthorIdentity :author="post.author" :created-at="post.createdAt" />
      </div>

      <div class="post-card__more">
        <button
          ref="moreButtonRef"
          class="post-card__metric post-card__more-button"
          type="button"
          aria-label="More actions"
          aria-haspopup="menu"
          :aria-expanded="moreOpen"
          @click.stop="toggleMore"
        >
          <AppIcon name="more" :size="20" />
        </button>
        <div
          v-if="moreOpen"
          ref="menuRef"
          class="post-card__menu"
          role="menu"
          @keydown="handleMenuKeydown"
        >
          <button
            :ref="setMenuItemRef"
            class="post-card__menu-item"
            type="button"
            role="menuitem"
            @click.stop="copyLink"
          >
            <AppIcon name="link" :size="18" />
            <span>{{ copyActionLabel }}</span>
          </button>
          <button
            v-if="showNotInterested"
            :ref="setMenuItemRef"
            class="post-card__menu-item"
            type="button"
            role="menuitem"
            @click.stop="handleNotInterested"
          >
            <AppIcon name="eye-off" :size="18" />
            <span>Not interested</span>
          </button>
          <button
            v-if="showDelete"
            :ref="setMenuItemRef"
            class="post-card__menu-item post-card__menu-item--danger"
            type="button"
            role="menuitem"
            :disabled="deletePending"
            :aria-busy="deletePending"
            @click.stop="handleDeletePost"
          >
            <AppIcon name="trash" :size="18" />
            <span>Delete post</span>
          </button>
        </div>
        <span
          v-if="copyState !== 'idle'"
          class="post-card__copy-status"
          aria-live="polite"
        >
          {{ copyActionLabel }}
        </span>
        <span
          v-if="deleteError"
          class="post-card__delete-status"
          role="status"
          aria-live="polite"
        >
          {{ deleteError }}
        </span>
      </div>
    </div>

    <RouterLink
      class="post-card__content"
      :to="{ name: 'PostDetail', params: { id: String(post.id) } }"
      @click.capture="prepareDetailNavigation"
    >
      <h2 v-if="post.title.trim()" class="post-card__title">{{ post.title }}</h2>
      <p
        v-if="post.excerpt"
        class="post-card__excerpt"
        :class="{ 'post-card__excerpt--standalone': !post.title.trim() }"
      >
        {{ post.excerpt }}
      </p>
      <div
        v-if="post.quotePost || post.replyToPost"
        class="post-card__reference"
        aria-label="Referenced post"
      >
        <span class="post-card__reference-label">
          {{ post.quotePost ? 'Quoted post' : 'Replying to' }}
        </span>
        <template v-if="referencePost?.deleted">
          <p class="post-card__reference-deleted">
            Post unavailable
          </p>
        </template>
        <template v-else>
          <AuthorIdentity
            v-if="referenceAuthor"
            :author="referenceAuthor"
            variant="compact"
          />
          <p class="post-card__reference-content">
            {{ referenceContent }}
          </p>
        </template>
      </div>
      <figure v-if="showCover" class="post-card__cover">
        <img
          :src="post.coverImageUrl"
          :alt="post.title.trim() || 'Post image'"
          loading="lazy"
          @error="hideCover"
        />
      </figure>
    </RouterLink>

    <div class="post-card__engagement" aria-label="Engagement">
      <RouterLink
        class="post-card__metric post-card__reply"
        :to="{
          name: 'PostDetail',
          params: { id: String(post.id) },
          query: { reply: '1' },
        }"
        :aria-label="replyLabel"
        @click.capture="prepareDetailNavigation"
      >
        <AppIcon name="reply" :size="18" />
        <span>{{ post.replyCount }}</span>
      </RouterLink>
      <RepostAction
        :key="post.id"
        :reposted="post.repostStatus === 'ready' && post.reposted"
        :count="post.repostCount"
        :disabled="repostUnavailable"
        :loading="repostLoading"
        :pending="repostPending"
        :ariaLabel="repostLabel"
        variant="compact"
        @toggle="handleRepostActivation"
      />
      <LikeAction
        :key="post.id"
        :liked="post.likeStatus === 'ready' ? post.liked : false"
        :count="post.likeCount"
        :disabled="likeUnavailable"
        :loading="likeLoading"
        :pending="likePending"
        :ariaLabel="likeLabel"
        :aria-pressed="post.likeStatus === 'ready' ? post.liked : null"
        variant="compact"
        @toggle="handleLikeActivation"
      />
      <RouterLink
        class="post-card__metric post-card__views"
        :to="{
          name: 'PostDetail',
          params: { id: String(post.id) },
        }"
        :aria-label="viewActionLabel"
        :title="viewActionLabel"
        @click.capture="prepareDetailNavigation"
      >
        <AppIcon name="analytics" :size="18" />
        <span>{{ compactViewCount }}</span>
      </RouterLink>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import type { FeedPost } from '../../types/Feed';
import AuthorIdentity from '../AuthorIdentity.vue';
import LikeAction from '../engagement/LikeAction.vue';
import RepostAction from '../engagement/RepostAction.vue';
import AppIcon from '../icons/AppIcon.vue';
import { getPostViewTelemetry } from '../../services/postViewTelemetry';
import { usePostDetailHandoffStore } from '../../store/postDetailHandoff';
import { formatAccessibleEngagementCount, formatCompactEngagementCount } from '../../utils/engagementCount';

const props = withDefaults(defineProps<{
  post: FeedPost;
  trackView?: boolean;
  likePending?: boolean;
  repostPending?: boolean;
  showNotInterested?: boolean;
  showDelete?: boolean;
  deletePending?: boolean;
  deleteError?: string;
}>(), {
  trackView: true,
  likePending: false,
  repostPending: false,
  showNotInterested: false,
  showDelete: false,
  deletePending: false,
  deleteError: '',
});

const emit = defineEmits<{
  postClick: [post: FeedPost];
  toggleLike: [postId: number];
  toggleRepost: [postId: number];
  notInterested: [postId: number];
  deletePost: [postId: number];
}>();

const router = useRouter();
const postViewTelemetry = getPostViewTelemetry();
const postDetailHandoff = usePostDetailHandoffStore();
const postCardRef = ref<HTMLElement | null>(null);
const showCover = ref(Boolean(props.post.coverImageUrl));
const moreButtonRef = ref<HTMLButtonElement | null>(null);
const menuRef = ref<HTMLDivElement | null>(null);
const menuItemRefs = ref<HTMLButtonElement[]>([]);
const moreOpen = ref(false);
type CopyState = 'idle' | 'success' | 'error';
const copyState = ref<CopyState>('idle');
let copyRequestVersion = 0;

const likeLoading = computed(() => props.post.likeStatus === 'unknown');
const likeUnavailable = computed(() => props.post.likeStatus === 'unavailable');
const repostLoading = computed(() => props.post.repostStatus === 'unknown');
const repostUnavailable = computed(() => props.post.repostStatus === 'unavailable');
const referencePost = computed(() => props.post.quotePost ?? props.post.replyToPost ?? null);
const referenceAuthor = computed(() => (
  referencePost.value && !referencePost.value.deleted
    ? referencePost.value.author
    : null
));
const referenceContent = computed(() => {
  const reference = referencePost.value;
  if (!reference || reference.deleted) {
    return '';
  }
  return reference.article?.title || reference.content || 'Post';
});

const handleLikeActivation = () => {
  if (props.post.likeStatus !== 'ready' || props.likePending) {
    return;
  }

  emit('toggleLike', props.post.id);
};

const handleRepostActivation = () => {
  if (props.post.repostStatus !== 'ready' || props.repostPending) {
    return;
  }

  emit('toggleRepost', props.post.id);
};

const actorLabel = (author: FeedPost['author']) => (
  author.display_name?.trim()
  || (author.username?.trim() ? '@' + author.username.trim() : '')
  || 'User'
);

const repostContextLabel = computed(() => (
  `${actorLabel(props.post.repostContext?.actor ?? props.post.author)} reposted`
));

const isNormalSameTabNavigation = (event: MouseEvent) => (
  event.button === 0
  && !event.ctrlKey
  && !event.metaKey
  && !event.shiftKey
  && !event.altKey
  && !event.defaultPrevented
);

const prepareDetailNavigation = (event: MouseEvent) => {
  if (isNormalSameTabNavigation(event)) {
    postDetailHandoff.remember(props.post);
  }
  emit('postClick', props.post);
};

const likeLabel = computed(() => {
  const countLabel = String(props.post.likeCount)
    + (props.post.likeCount === 1 ? ' like' : ' likes');
  if (props.post.likeStatus === 'unavailable') {
    return 'Like unavailable, ' + countLabel;
  }
  if (props.post.likeStatus !== 'ready' || !props.post.liked) {
    return 'Like post, ' + countLabel;
  }
  return 'Unlike post, ' + countLabel;
});

const replyLabel = computed(() => {
  const countLabel = String(props.post.replyCount)
    + (props.post.replyCount === 1 ? ' reply' : ' replies');
  return 'Reply to post, ' + countLabel;
});

const compactViewCount = computed(() => formatCompactEngagementCount(props.post.viewCount));
const viewLabel = computed(() => formatAccessibleEngagementCount(props.post.viewCount, 'views'));
const viewActionLabel = computed(() => 'Open post, ' + viewLabel.value);

const observeCurrentPost = () => {
  if (props.trackView && postCardRef.value) {
    postViewTelemetry.observeFeedCard(postCardRef.value, props.post.id);
  }
};

const unobserveCurrentPost = () => {
  if (postCardRef.value) {
    postViewTelemetry.unobserveFeedCard(postCardRef.value);
  }
};

const copyActionLabel = computed(() => {
  if (copyState.value === 'success') {
    return 'Copied';
  }
  if (copyState.value === 'error') {
    return 'Copy failed';
  }
  return 'Copy link';
});

const syncMenuItemRefs = () => {
  menuItemRefs.value = menuRef.value
    ? Array.from(menuRef.value.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'))
    : [];
};

const getMenuItems = () => {
  syncMenuItemRefs();
  return menuItemRefs.value;
};

const setMenuItemRef = () => {
  void nextTick(syncMenuItemRefs);
};

const focusMenuItem = (index: number) => {
  const items = getMenuItems();
  if (items.length === 0) {
    return;
  }
  items[(index + items.length) % items.length]?.focus();
};

const removeOutsideListener = () => {
  document.removeEventListener('pointerdown', handleDocumentPointerDown);
};

const closeMore = (restoreFocus = false) => {
  const wasOpen = moreOpen.value;
  copyRequestVersion += 1;
  moreOpen.value = false;
  copyState.value = 'idle';
  menuItemRefs.value = [];
  removeOutsideListener();
  if (restoreFocus && wasOpen) {
    void nextTick(() => moreButtonRef.value?.focus());
  }
};

const handleDocumentPointerDown = (event: PointerEvent) => {
  const target = event.target;
  if (!(target instanceof Node)) {
    return;
  }
  if (moreButtonRef.value?.contains(target) || menuRef.value?.contains(target)) {
    return;
  }
  closeMore();
};

const openMore = async () => {
  if (moreOpen.value) {
    return;
  }
  moreOpen.value = true;
  document.addEventListener('pointerdown', handleDocumentPointerDown);
  await nextTick();
  if (moreOpen.value) {
    focusMenuItem(0);
  }
};

const toggleMore = () => {
  if (moreOpen.value) {
    closeMore(true);
    return;
  }
  void openMore();
};

const handleMenuKeydown = (event: KeyboardEvent) => {
  const items = getMenuItems();
  if (items.length === 0) {
    return;
  }

  const currentIndex = items.indexOf(document.activeElement as HTMLButtonElement);
  if (event.key === 'ArrowDown') {
    event.preventDefault();
    focusMenuItem(currentIndex < 0 ? 0 : currentIndex + 1);
  } else if (event.key === 'ArrowUp') {
    event.preventDefault();
    focusMenuItem(currentIndex < 0 ? items.length - 1 : currentIndex - 1);
  } else if (event.key === 'Home') {
    event.preventDefault();
    focusMenuItem(0);
  } else if (event.key === 'End') {
    event.preventDefault();
    focusMenuItem(items.length - 1);
  } else if (event.key === 'Escape') {
    event.preventDefault();
    closeMore(true);
  } else if (event.key === 'Tab') {
    closeMore();
  }
};

const isCurrentCopyRequest = (requestVersion: number, postId: number) =>
  requestVersion === copyRequestVersion
  && postId === props.post.id
  && moreOpen.value;

const copyLink = async () => {
  const requestVersion = ++copyRequestVersion;
  const postId = props.post.id;

  try {
    const resolved = router.resolve({
      name: 'PostDetail',
      params: { id: String(postId) },
    });
    const url = new URL(resolved.href, window.location.origin).toString();
    if (!navigator.clipboard) {
      throw new Error('Clipboard API unavailable');
    }
    await navigator.clipboard.writeText(url);

    if (!isCurrentCopyRequest(requestVersion, postId)) {
      return;
    }

    copyState.value = 'success';
  } catch {
    if (!isCurrentCopyRequest(requestVersion, postId)) {
      return;
    }

    copyState.value = 'error';
  }
};

const handleNotInterested = () => {
  closeMore();
  emit('notInterested', props.post.id);
};

const handleDeletePost = () => {
  if (props.deletePending) {
    return;
  }
  if (!window.confirm('Delete this post? This cannot be undone.')) {
    return;
  }

  closeMore();
  emit('deletePost', props.post.id);
};

watch(
  () => props.post.coverImageUrl,
  (coverImageUrl) => {
    showCover.value = Boolean(coverImageUrl);
  },
);

watch(
  [() => props.post.id, () => props.trackView],
  ([postID, trackView], [previousPostID, previousTrackView]) => {
    if (postID === previousPostID && trackView === previousTrackView) {
      return;
    }
    unobserveCurrentPost();
    if (trackView) {
      observeCurrentPost();
    }
    if (postID !== previousPostID) {
      closeMore();
    }
  },
);

onMounted(observeCurrentPost);

const hideCover = () => {
  showCover.value = false;
};

onBeforeUnmount(() => {
  unobserveCurrentPost();
  closeMore();
});

const repostLabel = computed(() => {
  const countLabel = String(props.post.repostCount)
    + (props.post.repostCount === 1 ? ' repost' : ' reposts');
  if (props.post.repostStatus === 'unavailable') {
    return 'Repost unavailable, ' + countLabel;
  }
  if (props.post.repostStatus !== 'ready' || !props.post.reposted) {
    return 'Repost post, ' + countLabel;
  }
  return 'Undo repost, ' + countLabel;
});
</script>

<style scoped>
.post-card {
  --post-avatar-size: 40px;
  --post-column-gap: 12px;
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
  background: var(--color-surface);
}

.post-card__header {
  display: flex;
  align-items: center;
  min-width: 0;
}

.post-card__author-stack {
  min-width: 0;
  flex: 1 1 auto;
}

.post-card__repost-context {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0 0 4px calc(var(--post-avatar-size) + var(--post-column-gap));
  color: var(--color-text-tertiary);
  font-size: 12px;
  font-weight: 600;
  line-height: 1.2;
}

.post-card__header :deep(.author-identity) {
  flex: 1 1 auto;
  width: auto;
  min-width: 0;
  gap: var(--post-column-gap);
}

.post-card__header :deep(.author-avatar) {
  width: var(--post-avatar-size);
  height: var(--post-avatar-size);
  font-size: 15px;
}

.post-card__header :deep(.author-copy) {
  display: flex;
  align-items: baseline;
  min-width: 0;
  overflow: hidden;
  gap: var(--space-2);
  line-height: 1.2;
}

.post-card__header :deep(.author-name) {
  flex: 0 1 auto;
  min-width: 0;
  color: var(--color-text);
  font-size: 14px;
  font-weight: 700;
}

.post-card__header :deep(.author-meta) {
  flex: 1 1 0;
  min-width: 0;
  color: var(--color-text-tertiary);
  font-size: 13px;
  font-weight: 400;
}

.post-card__more {
  position: relative;
  flex: 0 0 auto;
  margin-left: var(--space-2);
}

.post-card__content,
.post-card__engagement {
  margin-left: calc(var(--post-avatar-size) + var(--post-column-gap));
}

.post-card__content {
  display: block;
  color: inherit;
  text-decoration: none;
}

.post-card__content:focus-visible {
  border-radius: var(--radius-sm);
  outline: 2px solid var(--color-accent);
  outline-offset: 3px;
}

.post-card__title {
  margin: var(--space-2) 0 0;
  color: var(--color-text);
  font-size: 15px;
  font-weight: 650;
  line-height: 1.45;
}

.post-card__excerpt {
  display: -webkit-box;
  max-height: calc(1.5em * 5);
  margin: var(--space-2) 0 0;
  overflow: hidden;
  color: var(--color-text);
  font-size: 15px;
  font-weight: 400;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 5;
  line-clamp: 5;
}

.post-card__excerpt--standalone {
  margin-top: 0;
}

.post-card__cover {
  box-sizing: border-box;
  aspect-ratio: 16 / 9;
  margin: var(--space-3) 0 0;
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  background: var(--color-surface-subtle);
}

.post-card__cover img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.post-card__engagement {
  display: flex;
  align-items: center;
  gap: var(--space-8);
  margin-top: var(--space-2);
  color: var(--color-text-tertiary);
  font-size: 13px;
}

@media (max-width: 799px) {
  .post-card__engagement {
    justify-content: space-between;
    gap: 0;
  }
}

.post-card__metric {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.post-card__reply,
.post-card__views {
  min-width: 40px;
  min-height: 40px;
  margin: -8px 0;
  border-radius: var(--radius-pill);
  padding: var(--space-1) var(--space-2);
}

.post-card__reply,
.post-card__views {
  color: inherit;
  text-decoration: none;
  transition: color 160ms ease, background-color 160ms ease, transform 160ms ease;
}

.post-card__more-button {
  min-width: 40px;
  min-height: 40px;
  margin: 0;
  border: 0;
  border-radius: var(--radius-pill);
  padding: var(--space-1) var(--space-2);
  background: transparent;
  color: inherit;
  cursor: pointer;
  font: inherit;
  transition: color 160ms ease, background-color 160ms ease, transform 160ms ease;
}

.post-card__menu {
  position: absolute;
  top: calc(100% + var(--space-1));
  right: 0;
  z-index: 20;
  display: grid;
  min-width: 148px;
  gap: 2px;
  padding: var(--space-1);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-sm);
  background: var(--color-surface);
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.12);
}

.post-card__menu-item {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 36px;
  border: 0;
  border-radius: calc(var(--radius-sm) - 2px);
  padding: 0 var(--space-3);
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
  text-align: left;
  white-space: nowrap;
}

.post-card__menu-item .app-icon {
  flex: 0 0 18px;
}

.post-card__menu-item:hover,
.post-card__menu-item:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

.post-card__menu-item--danger {
  color: var(--color-danger);
}

.post-card__menu-item--danger:hover:not(:disabled),
.post-card__menu-item--danger:focus-visible {
  color: var(--color-danger);
}

.post-card__menu-item--danger:disabled {
  cursor: wait;
  opacity: 0.64;
}

.post-card__copy-status {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
}

.post-card__delete-status {
  display: block;
  margin-top: var(--space-2);
  color: var(--color-danger);
  font-size: 12px;
}

.post-card__reply:hover,
.post-card__reply:focus-visible,
.post-card__views:hover,
.post-card__views:focus-visible,
.post-card__more-button:hover,
.post-card__more-button:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

.post-card__reply:active,
.post-card__views:active,
.post-card__more-button:active {
  transform: scale(0.97);
}

.post-card__reply .app-icon,
.post-card__views .app-icon {
  width: 18px;
  height: 18px;
}

.post-card__more-button .app-icon {
  width: 20px;
  height: 20px;
}

@media (max-width: 420px) {
  .post-card {
    --post-avatar-size: 36px;
    --post-column-gap: 10px;
    padding: var(--space-3);
  }

  .post-card__header :deep(.author-copy) {
    gap: var(--space-1);
  }

  .post-card__header :deep(.author-name) {
    font-size: 14px;
  }

  .post-card__header :deep(.author-meta) {
    font-size: 13px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .post-card__reply,
  .post-card__views,
  .post-card__more-button {
    transition: none;
  }
}
</style>
