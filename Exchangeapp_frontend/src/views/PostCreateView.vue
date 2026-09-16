<template>
  <main class="composer-view">
    <header class="composer-header">
      <button
        class="composer-header__back"
        type="button"
        aria-label="Back"
        @click="goBack"
      >
        <AppIcon name="arrow-left" :size="20" />
      </button>
      <h1>Post</h1>
    </header>

    <section
      v-if="!authStore.isAuthenticated"
      class="composer-auth-state"
      aria-labelledby="composer-login-heading"
    >
      <h2 id="composer-login-heading">Log in to create a post.</h2>
      <p>Your account is required to publish a post.</p>
      <RouterLink class="composer-action" :to="{ name: 'Login' }">Log in</RouterLink>
    </section>

    <form
      v-else
      id="composer-form"
      class="composer-form"
      novalidate
      @submit.prevent="submitPost"
    >
      <section class="composer-section" aria-labelledby="post-content-heading">
        <h2 id="post-content-heading" class="sr-only">Post content</h2>
        <div class="composer-main">
          <UserAvatar
            class="composer-author__avatar"
            :avatar-url="currentIdentity?.avatar_url"
            :display-name="currentIdentity?.display_name"
            :username="currentIdentity?.username"
            :size="42"
            loading="eager"
            decorative
          />

          <div class="composer-main__content">
            <label class="sr-only" for="post-content">Post</label>
            <textarea
              id="post-content"
              ref="contentInput"
              v-model="content"
              class="composer-input"
              rows="5"
              placeholder="What's happening?"
              :disabled="isSubmitting"
              :aria-describedby="contentError ? 'post-content-error' : undefined"
              @input="syncContentSelection"
              @select="syncContentSelection"
              @click="syncContentSelection"
              @keyup="syncContentSelection"
              @focus="syncContentSelection"
            ></textarea>

            <PostMediaGrid
              v-if="previewMedia.length > 0"
              :media="previewMedia"
              removable
              :disabled="isSubmitting"
              @remove="removeMedia"
            />

            <div
              v-if="previewPreparationItems.length > 0"
              class="composer-media-preparation"
            >
              <p
                v-if="previewPreparationPendingCount > 0"
                class="composer-media-preparation__status"
                aria-live="polite"
              >
                {{ previewPreparationPendingCount === 1
                  ? 'Preparing image...'
                  : `Preparing ${previewPreparationPendingCount} images...` }}
              </p>
              <ul class="composer-media-preparation__list">
                <li
                  v-for="item in previewPreparationItems"
                  :key="item.id"
                  class="composer-media-preparation__item"
                >
                  <span>
                    {{ item.status === 'error' ? item.error : 'Preparing image...' }}
                  </span>
                  <button
                    type="button"
                    class="composer-media-preparation__remove"
                    :disabled="isSubmitting"
                    @click="removeMediaByID(item.id)"
                  >
                    Remove image {{ item.index + 1 }}
                  </button>
                </li>
              </ul>
            </div>

            <p
              v-if="contentError"
              id="post-content-error"
              class="field-error content-error"
              role="alert"
            >
              {{ contentError }}
            </p>
            <p v-if="mediaError" class="field-error media-error" role="alert">
              {{ mediaError }}
            </p>

            <p v-if="publishError" class="field-error composer-validation-error" role="alert">
              {{ publishError }}
            </p>

            <p
              v-if="publishBlocked"
              id="publish-blocked-message"
              class="publish-blocked-message"
              role="status"
            >
              Another post is still sending. Wait for it to finish or retry it before posting this draft.
            </p>

            <div class="composer-toolbar">
              <div class="composer-toolbar__tools">
                <label
                  class="composer-tool media-picker"
                  :class="{ 'composer-tool--disabled': isSubmitting }"
                  :aria-disabled="isSubmitting"
                  aria-label="Add images"
                  title="Add images"
                  for="post-media-input"
                >
                  <AppIcon name="image" :size="20" />
                  <input
                    id="post-media-input"
                    class="media-input"
                    type="file"
                    multiple
                    accept="image/jpeg,image/png,image/webp"
                    :disabled="isSubmitting"
                    @change="handleMediaChange"
                  />
                </label>
                <button
                  ref="emojiButton"
                  class="composer-tool emoji-picker-trigger"
                  type="button"
                  aria-label="Add emoji"
                  title="Add emoji"
                  aria-haspopup="dialog"
                  :aria-expanded="emojiPickerOpen"
                  :aria-controls="emojiPickerId"
                  :disabled="isSubmitting"
                  @click="toggleEmojiPicker"
                >
                  <AppIcon name="smile" :size="20" />
                </button>
              </div>

              <div class="composer-toolbar__actions">
                <span
                  v-if="showCharacterCount"
                  class="composer-character-count"
                  :class="{ 'composer-character-count--over': remainingCharacters < 0 }"
                >
                  {{ remainingCharacters }}
                </span>
                <button
                  class="publish-button"
                  type="submit"
                  :disabled="!canPublish || isSubmitting || publishBlocked"
                  :aria-describedby="publishBlocked ? 'publish-blocked-message' : undefined"
                  :aria-busy="isSubmitting"
                >
                  {{ publishLabel }}
                </button>
              </div>
            </div>

            <EmojiPickerPopover
              :id="emojiPickerId"
              :open="emojiPickerOpen"
              :anchor-el="emojiButton"
              :disabled="isSubmitting"
              @select="insertEmoji"
              @close="handleEmojiPickerClose"
            />
          </div>
        </div>
      </section>

      <span v-if="isSubmitting" class="sr-only" aria-live="polite">
        {{ publishLabel }}
      </span>
    </form>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useAuthStore } from '../store/auth';
import { usePostDraftStore } from '../store/postDraft';
import { usePostPublishStore } from '../store/postPublish';
import AppIcon from '../components/icons/AppIcon.vue';
import PostMediaGrid from '../components/content/PostMediaGrid.vue';
import UserAvatar from '../components/users/UserAvatar.vue';
import EmojiPickerPopover, {
  type EmojiPickerCloseReason,
} from '../components/composer/EmojiPickerPopover.vue';
import type { PostMedia } from '../types/Post';
import {
  createLocalImagePreviewGenerator,
  type LocalImagePreviewGenerator,
} from '../utils/localImagePreview';
import { insertTextAtSelection } from '../utils/textareaInsertion';

const maxContentLength = 10000;
const maxMediaCount = 4;
const maxMediaBytes = 5 * 1024 * 1024;
const maxContentHeight = 360;
const allowedMediaTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);

const router = useRouter();
const authStore = useAuthStore();
const postDraft = usePostDraftStore();
const postPublishStore = usePostPublishStore();

const validationAttempted = ref(false);
const mediaError = ref('');
const publishError = ref('');
const contentInput = ref<HTMLTextAreaElement | null>(null);
const emojiButton = ref<HTMLButtonElement | null>(null);
const emojiPickerOpen = ref(false);
const selectionStart = ref<number | null>(null);
const selectionEnd = ref<number | null>(null);
type PreviewEntry = {
  file: File;
  url: string;
  width: number;
  height: number;
};

type PreviewState = {
  file: File;
  status: 'pending' | 'ready' | 'error';
  error?: string;
};

const previewEntries = ref(new Map<string, PreviewEntry>());
const previewStates = ref(new Map<string, PreviewState>());
const previewGenerator: LocalImagePreviewGenerator = createLocalImagePreviewGenerator({
  maxSide: 1024,
});
let previewLifecycleActive = true;
let previewQueueRunning = false;
const previewQueue: Array<{ id: string; file: File }> = [];
const queuedPreviewFiles = new Map<string, File>();
const emojiPickerId = 'post-emoji-picker';
const publishBlockedMessage = 'Another post is still sending. Wait for it to finish or retry it before posting this draft.';
const previewPreparationFailureMessage = 'Could not prepare this image preview. Remove the image and try again.';

const currentIdentity = computed(() => authStore.currentIdentity);
const currentUserID = computed(() => (
  authStore.isAuthenticated ? currentIdentity.value?.id ?? null : null
));
const currentPublishOperation = computed(() => (
  postDraft.publishOperationID
    ? postPublishStore.getOperation(postDraft.publishOperationID) || null
    : null
));
const content = computed({
  get: () => postDraft.content,
  set: (value: string) => postDraft.setContent(value),
});
const contentLength = computed(() => Array.from(content.value.trim()).length);
const remainingCharacters = computed(() => maxContentLength - contentLength.value);
const showCharacterCount = computed(() => remainingCharacters.value <= 1000);
const isSubmitting = computed(() => (
  currentPublishOperation.value?.phase === 'uploading'
  || currentPublishOperation.value?.phase === 'publishing'
));
const publishLabel = computed(() => {
  if (currentPublishOperation.value?.phase === 'uploading') {
    return 'Uploading...';
  }
  if (currentPublishOperation.value?.phase === 'publishing') {
    return 'Posting...';
  }
  return 'Post';
});
const publishBlocked = computed(() => {
  const viewerID = currentUserID.value;
  if (typeof viewerID !== 'number' || viewerID <= 0) {
    return false;
  }
  return postPublishStore.isDraftBlockedByAnotherPublish(
    viewerID,
    postDraft.publishOperationID,
  );
});
const contentError = computed(() => {
  if (contentLength.value > maxContentLength) {
    return 'Post must be ' + maxContentLength + ' characters or fewer.';
  }
  if (validationAttempted.value && !content.value.trim()) {
    return 'Post is required.';
  }
  return '';
});
const canPublish = computed(() => (
  authStore.isAuthenticated
  && Boolean(content.value.trim())
  && contentLength.value <= maxContentLength
  && !previewPreparationBlocked.value
  && !publishBlocked.value
));

const syncContentSelection = () => {
  const input = contentInput.value;
  if (!input) {
    return;
  }
  selectionStart.value = input.selectionStart;
  selectionEnd.value = input.selectionEnd;
};

const focusContentInput = async () => {
  await nextTick();
  const input = contentInput.value;
  if (!input || input.disabled) {
    return;
  }
  const start = selectionStart.value ?? input.value.length;
  const end = selectionEnd.value ?? start;
  input.focus({ preventScroll: true });
  input.setSelectionRange(start, end);
  selectionStart.value = start;
  selectionEnd.value = end;
};

const handleEmojiPickerClose = (reason: EmojiPickerCloseReason) => {
  emojiPickerOpen.value = false;
  if (reason === 'escape') {
    void focusContentInput();
  }
};

const toggleEmojiPicker = () => {
  if (isSubmitting.value) {
    return;
  }
  syncContentSelection();
  emojiPickerOpen.value = !emojiPickerOpen.value;
};

const insertEmoji = async (emoji: string) => {
  if (isSubmitting.value) {
    return;
  }
  const result = insertTextAtSelection(
    content.value,
    emoji,
    selectionStart.value,
    selectionEnd.value,
  );
  content.value = result.value;
  selectionStart.value = result.caret;
  selectionEnd.value = result.caret;
  await nextTick();
  const input = contentInput.value;
  if (!input || input.disabled) {
    return;
  }
  input.focus({ preventScroll: true });
  input.setSelectionRange(result.caret, result.caret);
  selectionStart.value = result.caret;
  selectionEnd.value = result.caret;
  resizeContent();
};

const previewMedia = computed<PostMedia[]>(() => postDraft.media
  .map((item, index) => {
    const entry = previewEntries.value.get(item.id);
    if (!entry || entry.file !== item.file) {
      return null;
    }
    return {
      type: 'image' as const,
      url: entry.url,
      large_url: entry.url,
      width: entry.width,
      height: entry.height,
      position: index,
    };
  })
  .filter((item): item is PostMedia => item !== null));

const previewPreparationItems = computed(() => postDraft.media
  .map((item, index) => {
    const state = previewStates.value.get(item.id);
    const entry = previewEntries.value.get(item.id);
    const sameStateFile = state?.file === item.file;
    const status = sameStateFile && state?.status === 'error'
      ? 'error' as const
      : sameStateFile && state?.status === 'ready' && entry?.file === item.file
        ? 'ready' as const
        : 'pending' as const;
    return {
      id: item.id,
      index,
      status,
      error: sameStateFile && state?.status === 'error'
        ? state.error || previewPreparationFailureMessage
        : undefined,
    };
  })
  .filter(item => item.status !== 'ready'));

const previewPreparationPendingCount = computed(() => previewPreparationItems.value
  .filter(item => item.status === 'pending').length);

const previewPreparationError = computed(() => previewPreparationItems.value
  .find(item => item.status === 'error')?.error || '');

const previewPreparationBlocked = computed(() => previewPreparationItems.value.length > 0);

const revokePreview = (entry: Pick<PreviewEntry, 'url'>) => {
  if (
    entry.url
    && typeof URL !== 'undefined'
    && typeof URL.revokeObjectURL === 'function'
  ) {
    URL.revokeObjectURL(entry.url);
  }
};

const isCurrentPreview = (id: string, file: File) => (
  previewLifecycleActive
  && postDraft.media.some(item => item.id === id && item.file === file)
);

const preparePreview = async (id: string, file: File) => {
  let createdURL = '';
  try {
    const preview = await previewGenerator.generate(file);
    if (!isCurrentPreview(id, file)) {
      return;
    }
    if (typeof URL === 'undefined' || typeof URL.createObjectURL !== 'function') {
      throw new Error('This browser cannot create a local image preview.');
    }
    createdURL = URL.createObjectURL(preview.blob);
    if (!isCurrentPreview(id, file)) {
      revokePreview({ url: createdURL });
      createdURL = '';
      return;
    }

    const previousEntry = previewEntries.value.get(id);
    if (previousEntry && previousEntry.url !== createdURL) {
      revokePreview(previousEntry);
    }
    const nextEntries = new Map(previewEntries.value);
    nextEntries.set(id, {
      file,
      url: createdURL,
      width: preview.width,
      height: preview.height,
    });
    previewEntries.value = nextEntries;

    const nextStates = new Map(previewStates.value);
    nextStates.set(id, { file, status: 'ready' });
    previewStates.value = nextStates;
    createdURL = '';
  } catch {
    if (!isCurrentPreview(id, file)) {
      if (createdURL) {
        revokePreview({ url: createdURL });
      }
      return;
    }
    if (createdURL) {
      revokePreview({ url: createdURL });
    }
    const nextEntries = new Map(previewEntries.value);
    const previousEntry = nextEntries.get(id);
    if (previousEntry && previousEntry.file === file) {
      revokePreview(previousEntry);
      nextEntries.delete(id);
      previewEntries.value = nextEntries;
    }
    const nextStates = new Map(previewStates.value);
    nextStates.set(id, {
      file,
      status: 'error',
      error: previewPreparationFailureMessage,
    });
    previewStates.value = nextStates;
  }
};

const drainPreviewQueue = async () => {
  if (previewQueueRunning) {
    return;
  }
  previewQueueRunning = true;
  try {
    while (previewQueue.length > 0 && previewLifecycleActive) {
      const item = previewQueue.shift();
      if (!item) {
        continue;
      }
      if (!isCurrentPreview(item.id, item.file)) {
        if (queuedPreviewFiles.get(item.id) === item.file) {
          queuedPreviewFiles.delete(item.id);
        }
        continue;
      }
      try {
        await preparePreview(item.id, item.file);
      } finally {
        if (queuedPreviewFiles.get(item.id) === item.file) {
          queuedPreviewFiles.delete(item.id);
        }
      }
    }
  } finally {
    previewQueueRunning = false;
  }
};

const syncPreviews = () => {
  const currentItems = new Map(postDraft.media.map(item => [item.id, item]));
  const nextEntries = new Map(previewEntries.value);
  const nextStates = new Map(previewStates.value);
  const itemsToPrepare: Array<{ id: string; file: File }> = [];

  for (const [id, entry] of nextEntries) {
    const item = currentItems.get(id);
    if (!item || item.file !== entry.file) {
      revokePreview(entry);
      nextEntries.delete(id);
    }
  }

  for (const [id, state] of nextStates) {
    const item = currentItems.get(id);
    if (!item || item.file !== state.file) {
      nextStates.delete(id);
    }
  }

  for (const item of postDraft.media) {
    const state = nextStates.get(item.id);
    const entry = nextEntries.get(item.id);
    if (state?.file === item.file && entry?.file === item.file) {
      continue;
    }
    nextStates.set(item.id, { file: item.file, status: 'pending' });
    if (queuedPreviewFiles.get(item.id) !== item.file) {
      queuedPreviewFiles.set(item.id, item.file);
      itemsToPrepare.push({ id: item.id, file: item.file });
    }
  }

  previewEntries.value = nextEntries;
  previewStates.value = nextStates;
  previewQueue.push(...itemsToPrepare);
  void drainPreviewQueue();
};

const revokeAllPreviews = () => {
  previewEntries.value.forEach(revokePreview);
  previewEntries.value = new Map();
  previewStates.value = new Map();
};

const validateMediaFile = (file: File) => {
  if (file.size <= 0) {
    return 'Choose a non-empty image file.';
  }
  if (!allowedMediaTypes.has(file.type)) {
    return 'Images must be JPEG, PNG, or WebP.';
  }
  if (file.size > maxMediaBytes) {
    return 'Each image must be 5 MB or smaller.';
  }
  return '';
};

const handleMediaChange = (event: Event) => {
  if (isSubmitting.value) {
    return;
  }

  const input = event.target as HTMLInputElement;
  const files = Array.from(input.files ?? []);
  let firstError = '';
  let overflow = false;

  for (const file of files) {
    if (postDraft.media.length >= maxMediaCount) {
      overflow = true;
      break;
    }
    const error = validateMediaFile(file);
    if (error) {
      firstError ||= error;
      continue;
    }
    postDraft.addMedia(file);
  }

  mediaError.value = overflow
    ? 'You can attach up to 4 images.'
    : firstError;
  publishError.value = '';
  input.value = '';
};

const removeMedia = (index: number) => {
  if (isSubmitting.value) {
    return;
  }
  const preview = previewMedia.value[index];
  const item = typeof preview?.position === 'number'
    ? postDraft.media[preview.position]
    : postDraft.media[index];
  if (item) {
    postDraft.removeMedia(item.id);
  }
  mediaError.value = '';
  publishError.value = '';
};

const removeMediaByID = (id: string) => {
  if (isSubmitting.value) {
    return;
  }
  if (postDraft.media.some(item => item.id === id)) {
    postDraft.removeMedia(id);
  }
  mediaError.value = '';
  publishError.value = '';
};

const resizeContent = () => {
  const input = contentInput.value;
  if (!input) {
    return;
  }
  input.style.height = 'auto';
  input.style.height = String(Math.min(input.scrollHeight, maxContentHeight)) + 'px';
  input.style.overflowY = input.scrollHeight > maxContentHeight ? 'auto' : 'hidden';
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

const submitPost = async () => {
  if (isSubmitting.value) {
    return;
  }

  emojiPickerOpen.value = false;

  validationAttempted.value = true;
  if (!canPublish.value) {
    if (publishBlocked.value) {
      publishError.value = publishBlockedMessage;
    } else if (previewPreparationBlocked.value) {
      publishError.value = previewPreparationError.value || 'Wait for image previews to finish before posting.';
    }
    return;
  }

  const publisherUserID = authStore.currentIdentity?.id;
  if (
    typeof publisherUserID !== 'number'
    || !Number.isSafeInteger(publisherUserID)
    || publisherUserID <= 0
  ) {
    publishError.value = 'Your account could not be verified. Your draft was preserved.';
    return;
  }
  publishError.value = '';

  const result = postPublishStore.startOrRetryDraft();
  if (result.status === 'rejected') {
    publishError.value = 'Your account could not be verified. Your draft was preserved.';
    return;
  }
  if (result.status === 'blocked') {
    publishError.value = publishBlockedMessage;
    return;
  }
  try {
    await router.replace({
      name: 'Home',
      query: { tab: 'for-you' },
    });
  } catch {
    // Navigation failure must not cancel the background publish operation.
  }
};

watch(content, () => {
  void nextTick(resizeContent);
});

watch(
  currentUserID,
  viewerID => {
    emojiPickerOpen.value = false;
    postDraft.setViewer(viewerID);
  },
  { immediate: true },
);

watch(() => postDraft.media, syncPreviews, { deep: true, immediate: true });

watch(isSubmitting, submitting => {
  if (submitting) {
    emojiPickerOpen.value = false;
  }
});

onMounted(() => {
  void nextTick(resizeContent);
});

onBeforeUnmount(() => {
  previewLifecycleActive = false;
  previewQueue.length = 0;
  queuedPreviewFiles.clear();
  emojiPickerOpen.value = false;
  previewGenerator.dispose();
  revokeAllPreviews();
});
</script>

<style scoped>
.composer-view {
  min-height: 100vh;
  color: var(--color-text);
  background: var(--color-surface);
}

.composer-header {
  position: sticky;
  top: 0;
  z-index: 12;
  display: grid;
  grid-template-columns: 40px minmax(0, 1fr) 40px;
  align-items: center;
  min-height: 52px;
  padding: 0 var(--space-4);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.composer-header h1 {
  grid-column: 2;
  justify-self: center;
  margin: 0;
  font-size: 18px;
  font-weight: 700;
  line-height: 1;
}

.composer-header__back {
  grid-column: 1;
  justify-self: start;
  display: grid;
  width: 40px;
  height: 40px;
  place-items: center;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0;
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  font-weight: 750;
}

.composer-header__back:focus-visible {
  background: var(--color-surface-subtle);
}

@media (hover: hover) and (pointer: fine) {
  .composer-header__back:hover {
    background: var(--color-surface-subtle);
  }
}

.publish-button {
  display: inline-flex;
  min-width: 68px;
  height: 36px;
  min-height: 36px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-4);
  background: var(--color-accent);
  color: var(--color-surface);
  cursor: pointer;
  font: inherit;
  font-size: 15px;
  font-weight: 700;
  line-height: 1;
  white-space: nowrap;
  transition: background var(--transition-fast), opacity var(--transition-fast);
}

.publish-button:focus-visible:not(:disabled) {
  background: var(--color-accent-hover);
}

@media (hover: hover) and (pointer: fine) {
  .publish-button:hover:not(:disabled) {
    background: var(--color-accent-hover);
  }
}

.publish-button:disabled {
  background: color-mix(in srgb, var(--color-accent) 42%, var(--color-surface));
  color: color-mix(in srgb, var(--color-surface) 90%, transparent);
  cursor: not-allowed;
}

.composer-form,
.composer-auth-state {
  border-bottom: 1px solid var(--color-border);
}

.composer-section {
  padding: var(--space-3) var(--space-4);
}

.composer-main {
  display: grid;
  grid-template-columns: 42px minmax(0, 1fr);
  align-items: start;
  gap: var(--space-3);
}

.composer-main__content {
  display: grid;
  min-width: 0;
  gap: 0;
}

.composer-author__avatar {
  margin-top: 2px;
  display: grid;
  width: 42px;
  height: 42px;
  place-items: center;
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: 50%;
  background: var(--color-surface-subtle);
}

.composer-author__avatar img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.composer-input {
  display: block;
  width: 100%;
  min-width: 0;
  min-height: 120px;
  max-height: 360px;
  border: 0;
  border-radius: 0;
  outline: none;
  box-shadow: none;
  padding: 4px 0 12px;
  background: transparent;
  color: var(--color-text);
  font: inherit;
  font-size: 20px;
  line-height: 1.4;
  resize: none;
  overflow-y: hidden;
  appearance: none;
}

.composer-input::placeholder {
  color: var(--color-text-tertiary);
  opacity: 1;
}

.composer-input:focus,
.composer-input:focus-visible {
  border: 0;
  outline: none;
  box-shadow: none;
}

.composer-input:disabled {
  cursor: not-allowed;
  opacity: 0.66;
}

.field-error {
  color: var(--color-danger);
}

.composer-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 52px;
  margin-top: 4px;
  padding-top: 8px;
  border-top: 1px solid var(--color-border);
}

.composer-toolbar__tools,
.composer-toolbar__actions {
  display: flex;
  align-items: center;
}

.composer-toolbar__tools {
  gap: 4px;
}

.composer-toolbar__actions {
  min-width: 0;
  gap: 12px;
  margin-left: auto;
}

.composer-tool {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0;
  background: transparent;
  color: var(--color-accent);
  cursor: pointer;
  font: inherit;
}

.composer-tool:focus-visible,
.composer-tool:focus-within {
  background: color-mix(in srgb, var(--color-accent) 10%, transparent);
}

@media (hover: hover) and (pointer: fine) {
  .composer-tool:hover:not(:disabled) {
    background: color-mix(in srgb, var(--color-accent) 10%, transparent);
  }
}

.composer-tool:disabled {
  cursor: not-allowed;
  opacity: 0.55;
  pointer-events: none;
}

.composer-tool:focus-within {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

.composer-tool--disabled {
  cursor: not-allowed;
  opacity: 0.55;
  pointer-events: none;
}

.composer-character-count {
  color: var(--color-text-tertiary);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}

.composer-character-count--over {
  color: var(--color-danger);
  font-weight: 750;
}

.media-picker {
  position: relative;
}

.emoji-picker-trigger {
  appearance: none;
}

.media-input {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}

.content-error,
.media-error,
.publish-blocked-message {
  margin: var(--space-2) 0 0;
  font-size: 13px;
}

.composer-media-preparation {
  display: grid;
  gap: 6px;
  margin-top: var(--space-2);
  color: var(--color-text-secondary);
  font-size: 13px;
}

.composer-media-preparation__status,
.composer-media-preparation__list {
  margin: 0;
}

.composer-media-preparation__list {
  display: grid;
  gap: 4px;
  padding: 0;
  list-style: none;
}

.composer-media-preparation__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.composer-media-preparation__item span {
  min-width: 0;
}

.composer-media-preparation__remove {
  flex: 0 0 auto;
  border: 0;
  padding: 0;
  background: transparent;
  color: var(--color-accent);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
}

.composer-media-preparation__remove:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.publish-blocked-message {
  color: var(--color-text-secondary);
}

.composer-action {
  display: inline-flex;
  min-height: 40px;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-pill);
  padding: var(--space-2) var(--space-4);
  background: var(--color-surface);
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  font-weight: 750;
  text-decoration: none;
}

.composer-action:focus-visible,
.composer-action:focus-within {
  border-color: var(--color-accent);
  color: var(--color-accent);
}

@media (hover: hover) and (pointer: fine) {
  .composer-action:hover {
    border-color: var(--color-accent);
    color: var(--color-accent);
  }
}

.composer-auth-state {
  padding: clamp(56px, 12vw, 120px) var(--space-5);
  text-align: center;
}

.composer-auth-state h2 {
  margin: 0;
  font-size: 24px;
}

.composer-auth-state p {
  margin: var(--space-2) 0 var(--space-5);
  color: var(--color-text-secondary);
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
  .composer-header {
    top: var(--mobile-safe-top);
  }
}

@media (max-width: 600px) {
  .composer-section {
    padding-inline: 12px;
  }

  .composer-main {
    gap: 10px;
  }
}

@media (max-width: 420px) {
  .composer-header,
  .composer-auth-state {
    padding-inline: var(--space-4);
  }

  .composer-section {
    padding-inline: 12px;
  }

  .composer-header {
    min-height: 54px;
  }

  .composer-main {
    grid-template-columns: 40px minmax(0, 1fr);
    gap: 10px;
  }

  .composer-author__avatar {
    width: 40px;
    height: 40px;
  }
}
</style>
