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
        <span>Create post</span>
      </button>

      <button
        class="publish-button"
        type="submit"
        form="composer-form"
        :disabled="!canPublish || isSubmitting"
      >
        {{ publishLabel }}
      </button>
    </header>

    <section
      v-if="!authStore.isAuthenticated"
      class="composer-auth-state"
      aria-labelledby="composer-login-heading"
    >
      <h1 id="composer-login-heading">Log in to create a post.</h1>
      <p>Your account is required to publish a post.</p>
      <RouterLink class="composer-action" :to="{ name: 'Login' }">Log in</RouterLink>
    </section>

    <form
      v-else
      id="composer-form"
      class="composer-form"
      novalidate
      @submit.prevent="submitArticle"
    >
      <section
        class="composer-section composer-section--main"
        aria-labelledby="post-fields-heading"
      >
        <h1 id="author-heading" class="sr-only">Current author</h1>
        <h2 id="post-fields-heading" class="sr-only">Post content</h2>

        <div class="composer-main">
          <span class="composer-author__avatar" aria-hidden="true">
            <img
              v-if="authorAvatarURL && !avatarLoadFailed"
              :src="authorAvatarURL"
              alt=""
              @error="avatarLoadFailed = true"
            />
            <span v-else>{{ authorInitial }}</span>
          </span>

          <div class="composer-main__content">
            <div class="composer-author__copy">
              <strong>{{ authorDisplayName }}</strong>
              <small>{{ authorHandle }}</small>
            </div>

            <div class="composer-field composer-field--primary">
              <label class="sr-only" for="article-content">Post</label>
              <textarea
                id="article-content"
                ref="contentInput"
                v-model="form.content"
                class="composer-input composer-input--content"
                rows="5"
                placeholder="What's happening?"
                :disabled="isSubmitting"
                aria-describedby="article-content-help article-content-error"
              ></textarea>
              <div id="article-content-help" class="composer-field__meta">
                <span
                  v-if="contentError"
                  id="article-content-error"
                  class="field-error"
                  role="alert"
                >
                  {{ contentError }}
                </span>
                <span :class="{ 'field-count--over': contentLength > maxContentLength }">
                  {{ contentLength }}/{{ maxContentLength }}
                </span>
              </div>
            </div>

            <details class="composer-details">
              <summary>Additional details</summary>
              <div class="composer-details__fields">
                <div class="composer-field">
                  <label for="article-title">Headline (optional)</label>
                  <input
                    id="article-title"
                    v-model="form.title"
                    class="composer-input composer-input--title"
                    type="text"
                    autocomplete="off"
                    placeholder="Headline"
                    :disabled="isSubmitting"
                    aria-describedby="article-title-help article-title-error"
                  />
                  <div id="article-title-help" class="composer-field__meta">
                    <span
                      v-if="titleError"
                      id="article-title-error"
                      class="field-error"
                      role="alert"
                    >
                      {{ titleError }}
                    </span>
                    <span :class="{ 'field-count--over': titleLength > maxTitleLength }">
                      {{ titleLength }}/{{ maxTitleLength }}
                    </span>
                  </div>
                </div>

                <div class="composer-field">
                  <label for="article-preview">Summary (optional)</label>
                  <textarea
                    id="article-preview"
                    v-model="form.preview"
                    class="composer-input composer-input--preview"
                    rows="2"
                    autocomplete="off"
                    placeholder="Add a short summary..."
                    :disabled="isSubmitting"
                    aria-describedby="article-preview-help article-preview-error"
                  ></textarea>
                  <div id="article-preview-help" class="composer-field__meta">
                    <span
                      v-if="previewError"
                      id="article-preview-error"
                      class="field-error"
                      role="alert"
                    >
                      {{ previewError }}
                    </span>
                    <span :class="{ 'field-count--over': previewLength > maxPreviewLength }">
                      {{ previewLength }}/{{ maxPreviewLength }}
                    </span>
                  </div>
                </div>
              </div>
            </details>
          </div>
        </div>
      </section>

      <section class="composer-section composer-section--cover" aria-labelledby="cover-heading">
        <div class="cover-heading">
          <div>
            <h2 id="cover-heading">Cover</h2>
            <p>Optional · JPEG, PNG, or WebP · up to 5 MB</p>
          </div>
        </div>

        <button
          v-if="!coverPreviewURL"
          class="cover-preview-frame cover-preview-frame--empty cover-preview-trigger"
          type="button"
          :disabled="isSubmitting"
          aria-label="Add cover image"
          @click="openCoverPicker"
        >
          <span class="cover-empty" aria-hidden="true">
            <AppIcon name="image" :size="28" />
            <span>No cover selected</span>
          </span>
        </button>
        <figure v-else class="cover-preview-frame">
          <img
            :src="coverPreviewURL"
            alt="Selected cover preview"
          />
        </figure>

        <div class="cover-actions">
          <label
            class="composer-action composer-action--secondary"
            :class="{ 'composer-action--disabled': isSubmitting }"
            :aria-disabled="isSubmitting"
            for="article-cover-input"
          >
            <AppIcon name="image" :size="16" />
            {{ coverFile ? 'Replace cover' : 'Add cover' }}
            <input
              ref="coverInput"
              id="article-cover-input"
              class="cover-input"
              type="file"
              accept="image/jpeg,image/png,image/webp"
              :disabled="isSubmitting"
              @change="handleCoverChange"
            />
          </label>
          <button
            v-if="coverFile"
            class="composer-action composer-action--secondary"
            type="button"
            :disabled="isSubmitting"
            @click="removeCover"
          >
            <AppIcon name="image-off" :size="16" />
            Remove
          </button>
        </div>

        <p v-if="coverError" class="field-error cover-error" role="alert">
          {{ coverError }}
        </p>
      </section>

      <div
        v-if="uploadError || publishError"
        class="composer-status"
        role="alert"
        aria-live="polite"
      >
        {{ uploadError || publishError }}
      </div>

      <p v-if="isSubmitting" class="composer-progress" aria-live="polite">
        {{ publishLabel }}
      </p>
    </form>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { createArticle, uploadArticleCover } from '../services/articleService';
import { getUser } from '../services/userService';
import { useAuthStore } from '../store/auth';
import { useFeedStore } from '../store/feed';
import AppIcon from '../components/icons/AppIcon.vue';
import type { PublicUser } from '../types/User';

type PublishPhase = 'idle' | 'uploading' | 'publishing';

const maxTitleLength = 80;
const maxPreviewLength = 180;
const maxContentLength = 10000;
const maxCoverBytes = 5 * 1024 * 1024;
const maxContentHeight = 360;
const allowedCoverTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);

const router = useRouter();
const authStore = useAuthStore();
const feedStore = useFeedStore();
const form = reactive({
  title: '',
  preview: '',
  content: '',
});

const phase = ref<PublishPhase>('idle');
const validationAttempted = ref(false);
const coverFile = ref<File | null>(null);
const coverPreviewURL = ref('');
const uploadedCoverURL = ref('');
const coverError = ref('');
const uploadError = ref('');
const publishError = ref('');
const coverInput = ref<HTMLInputElement | null>(null);
const contentInput = ref<HTMLTextAreaElement | null>(null);
const authorProfile = ref<PublicUser | null>(null);
const avatarLoadFailed = ref(false);
let authorProfileRequestVersion = 0;

const currentIdentity = computed(() => authStore.currentIdentity);
const currentUserID = computed(() => {
  if (!authStore.isAuthenticated) {
    return null;
  }

  return currentIdentity.value?.id ?? null;
});
const currentAuthorProfile = computed(() => {
  const identity = currentIdentity.value;
  const profile = authorProfile.value;

  if (!identity || !profile || profile.id !== identity.id) {
    return null;
  }

  return profile;
});
const authorUsername = computed(() => {
  const profileUsername = currentAuthorProfile.value?.username.trim() ?? '';

  return profileUsername || currentIdentity.value?.username.trim() || '';
});
const authorDisplayName = computed(() => {
  const displayName = currentAuthorProfile.value?.display_name.trim() ?? '';

  return displayName || authorUsername.value || 'Current user';
});
const authorHandle = computed(() => (
  authorUsername.value ? '@' + authorUsername.value : 'Signed-in account'
));
const authorInitial = computed(
  () => Array.from(authorDisplayName.value.trim() || authorUsername.value)[0]?.toUpperCase() || '?',
);
const authorAvatarURL = computed(
  () => currentAuthorProfile.value?.avatar_url.trim() ?? '',
);

const isSubmitting = computed(() => phase.value !== 'idle');
const publishLabel = computed(() => {
  if (phase.value === 'uploading') {
    return 'Uploading...';
  }
  if (phase.value === 'publishing') {
    return 'Publishing...';
  }
  return 'Publish';
});

const codePointLength = (value: string) => Array.from(value.trim()).length;
const titleLength = computed(() => codePointLength(form.title));
const previewLength = computed(() => codePointLength(form.preview));
const contentLength = computed(() => codePointLength(form.content));

const titleError = computed(() => {
  if (titleLength.value > maxTitleLength) {
    return 'Headline must be ' + maxTitleLength + ' characters or fewer.';
  }
  return '';
});

const previewError = computed(() => {
  if (previewLength.value > maxPreviewLength) {
    return 'Summary must be ' + maxPreviewLength + ' characters or fewer.';
  }
  return '';
});

const contentError = computed(() => {
  if (contentLength.value > maxContentLength) {
    return 'Post must be ' + maxContentLength + ' characters or fewer.';
  }
  if (validationAttempted.value && !form.content.trim()) {
    return 'Post is required.';
  }
  return '';
});

const canPublish = computed(() => (
  authStore.isAuthenticated
  && titleLength.value <= maxTitleLength
  && previewLength.value <= maxPreviewLength
  && Boolean(form.content.trim())
  && contentLength.value <= maxContentLength
));

const refreshAuthorProfile = async (userID: number | null) => {
  const requestVersion = ++authorProfileRequestVersion;

  authorProfile.value = null;
  avatarLoadFailed.value = false;

  if (typeof userID !== 'number' || !Number.isSafeInteger(userID) || userID <= 0) {
    return;
  }

  try {
    const profile = await getUser(userID);

    if (
      requestVersion !== authorProfileRequestVersion
      || currentUserID.value !== userID
      || profile.id !== userID
    ) {
      return;
    }

    authorProfile.value = profile;
  } catch {
    // Profile data enriches the composer but never blocks authoring or publishing.
  }
};

watch(
  currentUserID,
  userID => {
    void refreshAuthorProfile(userID);
  },
  { immediate: true },
);

watch(
  () => [currentAuthorProfile.value?.id, authorAvatarURL.value],
  () => {
    avatarLoadFailed.value = false;
  },
);

const revokeCoverPreview = () => {
  if (coverPreviewURL.value) {
    URL.revokeObjectURL(coverPreviewURL.value);
    coverPreviewURL.value = '';
  }
};

const removeCover = () => {
  if (isSubmitting.value) {
    return;
  }

  revokeCoverPreview();
  coverFile.value = null;
  uploadedCoverURL.value = '';
  coverError.value = '';
  uploadError.value = '';
  publishError.value = '';

  const input = document.getElementById('article-cover-input') as HTMLInputElement | null;
  if (input) {
    input.value = '';
  }
};

const openCoverPicker = () => {
  if (isSubmitting.value) {
    return;
  }

  coverInput.value?.click();
};

const handleCoverChange = (event: Event) => {
  if (isSubmitting.value) {
    return;
  }

  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file) {
    return;
  }

  if (file.size <= 0) {
    coverError.value = 'Choose a non-empty image file.';
    input.value = '';
    return;
  }

  if (!allowedCoverTypes.has(file.type)) {
    coverError.value = 'Cover must be JPEG, PNG, or WebP.';
    input.value = '';
    return;
  }

  if (file.size > maxCoverBytes) {
    coverError.value = 'Cover must be 5 MB or smaller.';
    input.value = '';
    return;
  }

  revokeCoverPreview();
  coverFile.value = file;
  coverPreviewURL.value = URL.createObjectURL(file);
  uploadedCoverURL.value = '';
  coverError.value = '';
  uploadError.value = '';
  publishError.value = '';
  input.value = '';
};

const resizeContent = () => {
  const input = contentInput.value;
  if (!input) {
    return;
  }

  input.style.height = 'auto';
  const nextHeight = Math.min(input.scrollHeight, maxContentHeight);
  input.style.height = String(nextHeight) + 'px';
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

const submitArticle = async () => {
  if (isSubmitting.value) {
    return;
  }

  validationAttempted.value = true;
  if (!canPublish.value) {
    return;
  }

  const publisherUserID = authStore.currentIdentity?.id;
  if (
    typeof publisherUserID !== 'number'
    || !Number.isFinite(publisherUserID)
    || publisherUserID <= 0
  ) {
    publishError.value = 'Your account could not be verified. Your draft was preserved.';
    return;
  }

  const selectedCover = coverFile.value;
  const draft = {
    title: form.title.trim(),
    preview: form.preview.trim(),
    content: form.content.trim(),
  };

  uploadError.value = '';
  publishError.value = '';

  try {
    let coverImageURL = uploadedCoverURL.value;

    if (selectedCover && !coverImageURL) {
      phase.value = 'uploading';
      try {
        const uploadedURL = (await uploadArticleCover(selectedCover)).trim();
        if (!uploadedURL) {
          throw new Error('The cover upload returned no URL.');
        }
        uploadedCoverURL.value = uploadedURL;
        coverImageURL = uploadedURL;
      } catch {
        uploadError.value = 'Cover upload failed. Your draft was preserved.';
        return;
      }
    }

    if (authStore.currentIdentity?.id !== publisherUserID) {
      publishError.value = 'Your account changed while publishing. No post was created, and your draft was preserved.';
      return;
    }

    phase.value = 'publishing';
    try {
      const article = await createArticle(
        coverImageURL
          ? { ...draft, cover_image_url: coverImageURL }
          : draft,
      );
      if (
        authStore.currentIdentity?.id !== publisherUserID
        || article.author?.id !== publisherUserID
      ) {
        publishError.value = 'The post was saved, but your account changed during publishing. It was not added to this Home feed.';
        return;
      }

      if (!feedStore.registerPublishedArticle(article, publisherUserID)) {
        publishError.value = 'The post was published, but Home could not update for this account. Your draft was preserved.';
        return;
      }

      await router.replace({
        name: 'Home',
        query: { tab: 'for-you' },
      });
    } catch {
      publishError.value = 'Post could not be published. Try again.';
    }
  } finally {
    phase.value = 'idle';
  }
};

watch(
  () => form.content,
  () => {
    void nextTick(resizeContent);
  },
);

onMounted(() => {
  void nextTick(resizeContent);
});

onBeforeUnmount(() => {
  ++authorProfileRequestVersion;
  revokeCoverPreview();
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
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 56px;
  padding: var(--space-2) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.composer-header__back {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 40px;
  border: 0;
  border-radius: var(--radius-pill);
  padding: var(--space-2) var(--space-3);
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  font-weight: 750;
}

.composer-header__back:hover {
  background: var(--color-surface-subtle);
}

.composer-header__back .app-icon {
  flex: 0 0 auto;
}

.publish-button {
  min-height: 40px;
  border: 0;
  border-radius: var(--radius-pill);
  padding: var(--space-2) var(--space-4);
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
  padding: var(--space-6) var(--space-5);
  border-bottom: 1px solid var(--color-border);
}

.composer-section--main {
  padding-top: var(--space-5);
  padding-bottom: var(--space-5);
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
  flex: 0 0 auto;
  place-items: center;
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: 50%;
  background: var(--color-surface-subtle);
  color: var(--color-text-secondary);
  font-size: 16px;
  font-weight: 800;
}

.composer-author__avatar img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.composer-author__copy {
  display: grid;
  min-width: 0;
  gap: 2px;
  line-height: 1.15;
}

.composer-author__copy strong,
.composer-author__copy small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.composer-author__copy strong {
  color: var(--color-text);
  font-size: 14px;
}

.composer-author__copy small {
  color: var(--color-text-tertiary);
  font-size: 12px;
}

.composer-details {
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-4);
}

.composer-details summary {
  color: var(--color-text-secondary);
  cursor: pointer;
  font-size: 14px;
  font-weight: 700;
}

.composer-details summary:focus-visible {
  border-radius: var(--radius-sm);
  outline: 2px solid color-mix(in srgb, var(--color-accent) 22%, transparent);
  outline-offset: 3px;
}

.composer-details__fields {
  display: grid;
  gap: var(--space-6);
  margin-top: var(--space-4);
}

.composer-field {
  display: grid;
  gap: var(--space-2);
}

.composer-field label,
.cover-heading h2 {
  color: var(--color-text);
  font-size: 14px;
  font-weight: 750;
}

.composer-input {
  display: block;
  width: 100%;
  min-width: 0;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-sm);
  padding: var(--space-3);
  background: var(--color-surface);
  color: var(--color-text);
  line-height: 1.5;
  resize: vertical;
}

.composer-input::placeholder {
  color: var(--color-text-tertiary);
}

.composer-input:focus {
  border-color: var(--color-accent);
  outline: 2px solid color-mix(in srgb, var(--color-accent) 22%, transparent);
  outline-offset: 1px;
}

.composer-input:disabled {
  cursor: not-allowed;
  opacity: 0.66;
}

.composer-input--title {
  font-size: 22px;
  font-weight: 650;
}

.composer-input--preview {
  min-height: 64px;
}

.composer-input--content {
  min-height: 140px;
  max-height: 360px;
  border-color: var(--color-border);
  font-size: 17px;
  overflow-y: hidden;
}

.composer-field__meta {
  display: flex;
  justify-content: space-between;
  gap: var(--space-3);
  min-height: 18px;
  color: var(--color-text-tertiary);
  font-size: 12px;
}

.field-count--over {
  color: var(--color-danger);
  font-weight: 700;
}

.field-error {
  color: var(--color-danger);
  font-size: 13px;
}

.cover-heading {
  display: flex;
  align-items: start;
  justify-content: space-between;
  gap: var(--space-3);
}

.cover-heading h2 {
  margin: 0;
}

.cover-heading p {
  margin: var(--space-1) 0 0;
  color: var(--color-text-tertiary);
  font-size: 13px;
}

.cover-preview-frame {
  display: grid;
  width: min(100%, 560px);
  aspect-ratio: 16 / 9;
  margin: var(--space-4) 0 0;
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-sm);
  background: var(--color-surface-subtle);
}

.cover-preview-frame img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.cover-preview-frame--empty {
  aspect-ratio: auto;
  min-height: 180px;
}

.cover-preview-trigger {
  appearance: none;
  padding: 0;
  text-align: inherit;
  font: inherit;
  color: inherit;
  cursor: pointer;
}

.cover-preview-trigger:hover:not(:disabled) {
  border-color: var(--color-accent);
}

.cover-preview-trigger:focus-visible {
  border-color: var(--color-accent);
  outline: 2px solid color-mix(in srgb, var(--color-accent) 22%, transparent);
  outline-offset: 2px;
}

.cover-preview-trigger:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.cover-empty {
  display: grid;
  place-content: center;
  justify-items: center;
  gap: var(--space-2);
  color: var(--color-text-tertiary);
  font-size: 13px;
}

.cover-empty .app-icon {
  flex: 0 0 auto;
}

.cover-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-3);
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

.composer-action--disabled {
  cursor: not-allowed;
  opacity: 0.55;
  pointer-events: none;
}

.composer-action:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.cover-input {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}

.cover-input:focus-visible + span {
  outline: 2px solid var(--color-accent);
  outline-offset: 3px;
}

.cover-error {
  margin: var(--space-3) 0 0;
}

.composer-status {
  padding: var(--space-4) var(--space-5) 0;
  color: var(--color-danger);
  font-size: 14px;
}

.composer-progress {
  margin: 0;
  padding: var(--space-3) var(--space-5) var(--space-6);
  color: var(--color-text-secondary);
  font-size: 13px;
}

.composer-auth-state {
  padding: clamp(56px, 12vw, 120px) var(--space-5);
  text-align: center;
}

.composer-auth-state h1 {
  margin: 0;
  color: var(--color-text);
  font-size: 24px;
  letter-spacing: -0.02em;
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
  .composer-progress,
  .composer-auth-state {
    padding-inline: var(--space-4);
  }

  .composer-header {
    min-height: 54px;
  }

  .composer-input--title {
    font-size: 20px;
  }

  .composer-author__avatar {
    width: 38px;
    height: 38px;
  }

  .cover-preview-frame {
    width: 100%;
  }

  .cover-preview-frame--empty {
    min-height: 150px;
  }
}
</style>
