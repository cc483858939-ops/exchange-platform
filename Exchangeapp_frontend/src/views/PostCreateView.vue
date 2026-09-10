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
      <button
        class="publish-button"
        type="submit"
        form="composer-form"
        :disabled="!canPublish || isSubmitting"
        :aria-busy="isSubmitting"
      >
        {{ publishLabel }}
      </button>
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

            <div class="composer-toolbar">
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
              <span
                v-if="showCharacterCount"
                class="composer-character-count"
                :class="{ 'composer-character-count--over': remainingCharacters < 0 }"
              >
                {{ remainingCharacters }}
              </span>
            </div>

            <EmojiPickerPopover
              :id="emojiPickerId"
              :open="emojiPickerOpen"
              :anchor-el="emojiButton"
              :disabled="isSubmitting"
              @select="insertEmoji"
              @close="handleEmojiPickerClose"
            />

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
          </div>
        </div>
      </section>

      <div
        v-if="uploadError || publishError"
        class="composer-status"
        role="alert"
        aria-live="polite"
      >
        {{ uploadError || publishError }}
      </div>

      <span v-if="isSubmitting" class="sr-only" aria-live="polite">
        {{ publishLabel }}
      </span>
    </form>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { createPost, uploadPostMedia } from '../services/postService';
import { useAuthStore } from '../store/auth';
import { usePostDraftStore } from '../store/postDraft';
import { useFeedStore } from '../store/feed';
import { useProfileSessionStore } from '../store/profileSession';
import AppIcon from '../components/icons/AppIcon.vue';
import PostMediaGrid from '../components/content/PostMediaGrid.vue';
import UserAvatar from '../components/users/UserAvatar.vue';
import EmojiPickerPopover, {
  type EmojiPickerCloseReason,
} from '../components/composer/EmojiPickerPopover.vue';
import type { PostMedia } from '../types/Post';
import { insertTextAtSelection } from '../utils/textareaInsertion';

type PublishPhase = 'idle' | 'uploading' | 'publishing';

const maxContentLength = 10000;
const maxMediaCount = 4;
const maxMediaBytes = 5 * 1024 * 1024;
const maxContentHeight = 360;
const allowedMediaTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);

const router = useRouter();
const authStore = useAuthStore();
const postDraft = usePostDraftStore();
const feedStore = useFeedStore();
const profileSessionStore = useProfileSessionStore();

const phase = ref<PublishPhase>('idle');
const validationAttempted = ref(false);
const mediaError = ref('');
const uploadError = ref('');
const publishError = ref('');
const contentInput = ref<HTMLTextAreaElement | null>(null);
const emojiButton = ref<HTMLButtonElement | null>(null);
const emojiPickerOpen = ref(false);
const selectionStart = ref<number | null>(null);
const selectionEnd = ref<number | null>(null);
const previewEntries = ref(new Map<string, { file: File; url: string }>());
const emojiPickerId = 'post-emoji-picker';
let publishAttemptVersion = 0;

const currentIdentity = computed(() => authStore.currentIdentity);
const currentUserID = computed(() => (
  authStore.isAuthenticated ? currentIdentity.value?.id ?? null : null
));
const content = computed({
  get: () => postDraft.content,
  set: (value: string) => postDraft.setContent(value),
});
const contentLength = computed(() => Array.from(content.value.trim()).length);
const remainingCharacters = computed(() => maxContentLength - contentLength.value);
const showCharacterCount = computed(() => remainingCharacters.value <= 1000);
const isSubmitting = computed(() => phase.value !== 'idle');
const publishLabel = computed(() => {
  if (phase.value === 'uploading') {
    return 'Uploading...';
  }
  if (phase.value === 'publishing') {
    return 'Posting...';
  }
  return 'Post';
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
  .map((item, index) => ({
    type: 'image' as const,
    url: previewEntries.value.get(item.id)?.url || '',
    position: index,
  }))
  .filter(item => Boolean(item.url)));

const revokePreview = (entry: { file: File; url: string }) => {
  if (
    entry.url
    && typeof URL !== 'undefined'
    && typeof URL.revokeObjectURL === 'function'
  ) {
    URL.revokeObjectURL(entry.url);
  }
};

const syncPreviews = () => {
  const currentItems = new Map(postDraft.media.map(item => [item.id, item]));
  const nextEntries = new Map(previewEntries.value);

  for (const [id, entry] of nextEntries) {
    const item = currentItems.get(id);
    if (!item || item.file !== entry.file) {
      revokePreview(entry);
      nextEntries.delete(id);
    }
  }

  for (const item of postDraft.media) {
    if (nextEntries.has(item.id)) {
      continue;
    }
    const createObjectURL = typeof URL !== 'undefined'
      && typeof URL.createObjectURL === 'function'
      ? URL.createObjectURL.bind(URL)
      : null;
    if (createObjectURL) {
      nextEntries.set(item.id, { file: item.file, url: createObjectURL(item.file) });
    }
  }

  previewEntries.value = nextEntries;
};

const revokeAllPreviews = () => {
  previewEntries.value.forEach(revokePreview);
  previewEntries.value = new Map();
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
  uploadError.value = '';
  publishError.value = '';
  input.value = '';
};

const removeMedia = (index: number) => {
  if (isSubmitting.value) {
    return;
  }
  const item = postDraft.media[index];
  if (item) {
    postDraft.removeMedia(item.id);
  }
  mediaError.value = '';
  uploadError.value = '';
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

const isCurrentPublishAttempt = (
  attemptVersion: number,
  publisherUserID: number,
  selectedMedia: Array<{ id: string; file: File }>,
) => (
  publishAttemptVersion === attemptVersion
  && authStore.isAuthenticated
  && authStore.currentIdentity?.id === publisherUserID
  && postDraft.viewerID === publisherUserID
  && postDraft.media.length === selectedMedia.length
  && selectedMedia.every((item, index) => (
    postDraft.media[index]?.id === item.id
    && postDraft.media[index]?.file === item.file
  ))
);

const submitPost = async () => {
  if (isSubmitting.value) {
    return;
  }

  emojiPickerOpen.value = false;

  validationAttempted.value = true;
  if (!canPublish.value) {
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

  const publishAttempt = ++publishAttemptVersion;
  const selectedMedia = postDraft.media.map(item => ({
    id: item.id,
    file: item.file,
    uploadedURL: item.uploadedURL.trim(),
  }));
  const draftContent = content.value.trim();
  const attemptMediaIdentity = selectedMedia.map(item => ({ id: item.id, file: item.file }));
  const uploadedURLs: string[] = [];

  uploadError.value = '';
  publishError.value = '';

  for (const item of selectedMedia) {
    let uploadedURL = item.uploadedURL;
    if (!uploadedURL) {
      phase.value = 'uploading';
      try {
        uploadedURL = (await uploadPostMedia(item.file)).trim();
        if (!uploadedURL) {
          throw new Error('The media upload returned no URL.');
        }
        if (!isCurrentPublishAttempt(publishAttempt, publisherUserID, attemptMediaIdentity)) {
          return;
        }
        postDraft.setUploadedURL(item.id, uploadedURL);
      } catch {
        if (publishAttemptVersion === publishAttempt) {
          uploadError.value = 'Image upload failed. Your draft was preserved.';
        }
        phase.value = 'idle';
        return;
      }
    }
    uploadedURLs.push(uploadedURL);
  }

  if (!isCurrentPublishAttempt(publishAttempt, publisherUserID, attemptMediaIdentity)) {
    publishError.value = 'Your account changed while posting. No post was created, and your draft was preserved.';
    return;
  }

  phase.value = 'publishing';
  try {
    const post = await createPost({
      content: draftContent,
      media: uploadedURLs.map(url => ({ type: 'image' as const, url })),
    });
    if (!isCurrentPublishAttempt(publishAttempt, publisherUserID, attemptMediaIdentity)) {
      return;
    }
    if (post.author?.id !== publisherUserID) {
      publishError.value = 'The post was saved, but your account changed during posting. It was not added to this Home feed.';
      return;
    }

    if (!feedStore.registerPublishedPost(post, publisherUserID)) {
      publishError.value = 'The post was posted, but Home could not update for this account. Your draft was preserved.';
      return;
    }
    profileSessionStore.registerPublishedTimelinePost(post, publisherUserID);
    authStore.syncCurrentIdentityProfile(post.author);
    postDraft.clear();

    await router.replace({
      name: 'Home',
      query: { tab: 'for-you' },
    });
  } catch {
    publishError.value = 'Post could not be posted. Try again.';
  } finally {
    if (publishAttemptVersion === publishAttempt) {
      phase.value = 'idle';
    }
  }
};

watch(content, () => {
  void nextTick(resizeContent);
});

watch(
  currentUserID,
  viewerID => {
    publishAttemptVersion += 1;
    emojiPickerOpen.value = false;
    postDraft.setViewer(viewerID);
    phase.value = 'idle';
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
  publishAttemptVersion += 1;
  emojiPickerOpen.value = false;
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
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
  min-height: 56px;
  padding: var(--space-2) var(--space-4);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.composer-header h1 {
  margin: 0;
  font-size: 18px;
  font-weight: 800;
}

.composer-header__back {
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

.composer-header__back:hover,
.composer-header__back:focus-visible {
  background: var(--color-surface-subtle);
}

.publish-button {
  justify-self: end;
  min-height: 36px;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-4);
  background: var(--color-accent);
  color: var(--color-surface);
  cursor: pointer;
  font: inherit;
  font-weight: 800;
  transition: background var(--transition-fast), opacity var(--transition-fast);
}

.publish-button:hover:not(:disabled),
.publish-button:focus-visible:not(:disabled) {
  background: var(--color-accent-hover);
}

.publish-button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
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
  grid-template-columns: auto minmax(0, 1fr);
  align-items: start;
  gap: var(--space-3);
}

.composer-main__content {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
}

.composer-author__avatar {
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
  padding: 0;
  background: transparent;
  color: var(--color-text);
  font: inherit;
  font-size: 20px;
  line-height: 1.45;
  resize: none;
  overflow-y: hidden;
}

.composer-input::placeholder {
  color: var(--color-text-tertiary);
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
  min-height: 40px;
  align-items: center;
  gap: var(--space-3);
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

.composer-tool:hover,
.composer-tool:focus-within {
  background: color-mix(in srgb, var(--color-accent) 10%, transparent);
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
  margin-left: auto;
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
.media-error {
  margin: 0;
  font-size: 13px;
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

.composer-action:hover,
.composer-action:focus-visible,
.composer-action:focus-within {
  border-color: var(--color-accent);
  color: var(--color-accent);
}

.composer-status {
  padding: 0 var(--space-4) var(--space-3);
  color: var(--color-danger);
  font-size: 14px;
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

@media (max-width: 420px) {
  .composer-header,
  .composer-section,
  .composer-status,
  .composer-auth-state {
    padding-inline: var(--space-4);
  }

  .composer-header {
    min-height: 54px;
  }

  .composer-author__avatar {
    width: 40px;
    height: 40px;
  }
}
</style>
