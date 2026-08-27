<template>
  <main class="profile-view">
    <header class="profile-header">
      <button
        class="profile-header__back"
        type="button"
        aria-label="Back"
        @click="goBack"
      >
        <AppIcon name="arrow-left" :size="22" />
        <span class="profile-header__copy">
          <strong>{{ headerUsername }}</strong>
          <small>Profile</small>
        </span>
      </button>
      <MobileAccountMenu v-if="isOwnProfile" />
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
      <UserAvatar
        class="profile-avatar"
        :avatar-url="user.avatar_url"
        :display-name="user.display_name"
        :username="user.username"
        :size="76"
        decorative
      />
      <div class="profile-identity__copy">
        <h1 id="profile-name">{{ profileDisplayName }}</h1>
        <p class="profile-identity__handle">@{{ user.username }}</p>
        <p v-if="user.bio" class="profile-identity__bio">{{ user.bio }}</p>
        <time
          v-if="joinedLabel"
          class="profile-identity__joined"
          :datetime="user.created_at"
        >
          Joined {{ joinedLabel }}
        </time>
        <div v-if="socialReady" class="profile-social" aria-label="Social stats">
          <RouterLink :to="{ name: 'UserFollowing', params: { id: user.id } }">
            {{ followState?.following_count ?? 0 }} Following
          </RouterLink>
          <RouterLink :to="{ name: 'UserFollowers', params: { id: user.id } }">
            {{ followState?.follower_count ?? 0 }} Followers
          </RouterLink>
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
      <div v-if="isOwnProfile || showFollowControl" class="profile-identity__action">
        <button
          v-if="isOwnProfile"
          class="profile-action profile-action--primary"
          type="button"
          @click="openEditProfile"
        >
          Edit profile
        </button>
        <template v-else>
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
        </template>
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
            :repost-pending="repostPendingArticleIds.has(post.id)"
            :show-delete="canDeletePost(post)"
            :delete-pending="pendingDeleteArticleIds.has(post.id)"
            :delete-error="deleteErrors.get(post.id) || ''"
            @toggle-like="handleLikeToggle"
            @toggle-repost="handleRepostToggle"
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

  <dialog
      ref="editDialogRef"
      class="profile-edit-dialog"
      aria-labelledby="profile-edit-title"
      @cancel="handleDialogCancel"
      @close="handleDialogClose"
    >
      <form class="profile-edit-form" @submit.prevent="saveProfile">
        <header class="profile-edit-dialog__header">
          <h2 id="profile-edit-title">Edit profile</h2>
          <button
            class="profile-edit-dialog__close"
            type="button"
            aria-label="Close edit profile"
            :disabled="editSaving"
            @click="closeEditProfile"
          >
            <AppIcon name="close" :size="20" />
          </button>
        </header>

        <div class="profile-edit-avatar">
          <div class="profile-avatar profile-avatar--edit">
            <img
              v-if="editAvatarPreview"
              :src="editAvatarPreview"
              :alt="editProfileDisplayName + ' avatar preview'"
              @error="editAvatarLoadFailed = true"
            />
            <span v-else aria-hidden="true">{{ editProfileInitial }}</span>
          </div>
          <div class="profile-edit-avatar__copy">
            <span class="profile-edit-field__label">Avatar</span>
            <div class="profile-edit-avatar__actions">
              <label class="profile-action profile-action--compact" for="profile-avatar-input">
                <AppIcon name="camera" :size="16" />
                Change photo
                <input
                  id="profile-avatar-input"
                  ref="profileAvatarInputRef"
                  class="sr-only"
                  type="file"
                  accept="image/jpeg,image/png,image/webp"
                  :disabled="editSaving"
                  @change="handleAvatarSelection"
                />
              </label>
              <button
                class="profile-action profile-action--compact"
                type="button"
                :disabled="editSaving || !editAvatarHasValue"
                @click="removeProfileAvatar"
              >
                <AppIcon name="image-off" :size="16" />
                Remove photo
              </button>
            </div>
            <p v-if="editAvatarError" class="profile-edit-error" role="alert">{{ editAvatarError }}</p>
          </div>
        </div>

        <div class="profile-edit-field">
          <div class="profile-edit-field__label-row">
            <label for="profile-display-name">Display name</label>
            <span>{{ editDisplayNameLength }}/50</span>
          </div>
          <input
            id="profile-display-name"
            ref="editDisplayNameInputRef"
            v-model="editDraft.display_name"
            type="text"
            autocomplete="name"
            :disabled="editSaving"
            aria-describedby="profile-display-name-help"
          />
          <p id="profile-display-name-help" v-if="editDisplayNameOverLimit" class="profile-edit-error" role="alert">
            Display name must be 50 characters or fewer.
          </p>
        </div>

        <div class="profile-edit-field profile-edit-field--readonly">
          <span class="profile-edit-field__label">Username</span>
          <strong>@{{ user?.username }}</strong>
          <small>Username can't be changed.</small>
        </div>

        <div class="profile-edit-field">
          <div class="profile-edit-field__label-row">
            <label for="profile-bio">Bio</label>
            <span>{{ editBioLength }}/160</span>
          </div>
          <textarea
            id="profile-bio"
            v-model="editDraft.bio"
            rows="4"
            :disabled="editSaving"
            aria-describedby="profile-bio-help"
          ></textarea>
          <p id="profile-bio-help" v-if="editBioOverLimit" class="profile-edit-error" role="alert">
            Bio must be 160 characters or fewer.
          </p>
        </div>

        <p v-if="editError" class="profile-edit-error" role="alert" aria-live="polite">{{ editError }}</p>

        <footer class="profile-edit-dialog__actions">
          <button class="profile-action" type="button" :disabled="editSaving" @click="closeEditProfile">
            Cancel
          </button>
          <button class="profile-follow-button" type="submit" :disabled="!editCanSave" :aria-busy="editSaving">
            {{ editSaving ? 'Saving…' : 'Save' }}
          </button>
        </footer>
      </form>
    </dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import PostCard from '../components/feed/PostCard.vue';
import AppIcon from '../components/icons/AppIcon.vue';
import MobileAccountMenu from '../components/layout/MobileAccountMenu.vue';
import UserAvatar from '../components/users/UserAvatar.vue';
import { updateUserProfile, uploadProfileAvatar } from '../services/userService';
import type { UpdateUserProfilePayload, UserFollowState } from '../services/userService';
import { useAuthStore } from '../store/auth';
import { useProfileSessionStore, type ProfileSessionCapture } from '../store/profileSession';
import type { PublicUser } from '../types/User';

const profileDisplayNameLimit = 50;
const profileBioLimit = 160;
const profileAvatarMaxBytes = 2 * 1024 * 1024;
const profileAvatarTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);
const skeletonCount = 3;

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const profileStore = useProfileSessionStore();

const userId = computed(() => String(route.params.id ?? '').trim());
const numericUserID = computed(() => {
  const value = Number(userId.value);
  return Number.isSafeInteger(value) && value > 0 ? value : null;
});
const activeSession = computed(() => (
  numericUserID.value === null ? null : profileStore.ensureSession(numericUserID.value)
));
const invalidProfileError = ref('');

const user = computed(() => activeSession.value?.user ?? null);
const profileLoading = computed(() => activeSession.value?.profileLoading ?? false);
const profileError = computed(() => invalidProfileError.value || activeSession.value?.profileError || '');
const profileNotFound = computed(() => activeSession.value?.profileNotFound ?? false);
const articles = computed(() => activeSession.value?.articles ?? []);
const articlesInitialLoading = computed(() => activeSession.value?.articlesInitialLoading ?? false);
const articlesLoadingMore = computed(() => activeSession.value?.articlesLoadingMore ?? false);
const articlesInitialError = computed(() => activeSession.value?.articlesInitialError ?? '');
const articlesLoadMoreError = computed(() => activeSession.value?.articlesLoadMoreError ?? '');
const nextCursor = computed(() => activeSession.value?.nextCursor ?? null);
const hasMore = computed(() => activeSession.value?.hasMore ?? false);
const followState = computed<UserFollowState | null>(() => activeSession.value?.followState ?? null);
const followLoading = computed(() => activeSession.value?.followLoading ?? false);
const followError = computed(() => activeSession.value?.followError ?? '');
const followActionError = computed(() => activeSession.value?.followActionError ?? '');
const followPending = computed(() => activeSession.value?.followPending ?? false);

const likePendingArticleIds = profileStore.likePendingArticleIds;
const repostPendingArticleIds = profileStore.repostPendingArticleIds;
const pendingDeleteArticleIds = profileStore.pendingDeleteArticleIds;
const deleteErrors = profileStore.deleteErrors;

const sentinelRef = ref<HTMLElement | null>(null);
const intersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
let observer: IntersectionObserver | null = null;
let profileEntryVersion = 0;
let restoredEntryVersion = -1;

const profileDisplayName = computed(() => {
  const displayName = user.value?.display_name?.trim() ?? '';
  return displayName || user.value?.username || 'Profile';
});
const headerUsername = computed(() => profileDisplayName.value);
const joinedLabel = computed(() => {
  const value = user.value?.created_at;
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleDateString(undefined, { month: 'long', year: 'numeric' });
});

const getErrorStatus = (error: unknown) =>
  (error as { response?: { status?: number } }).response?.status;

const currentViewerID = computed(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});
const isOwnProfile = computed(() => Boolean(
  user.value
  && currentViewerID.value !== null
  && user.value.id === currentViewerID.value,
));
const socialReady = computed(() => Boolean(
  authStore.isAuthenticated
  && currentViewerID.value !== null
  && followState.value
  && !followLoading.value
  && activeSession.value?.followLoaded,
));
const showFollowControl = computed(() => Boolean(
  authStore.isAuthenticated
  && user.value
  && currentViewerID.value !== null
  && user.value.id !== currentViewerID.value
  && socialReady.value,
));
const canDeletePost = (post: { author: { id: number } }) =>
  authStore.isAuthenticated
  && currentViewerID.value !== null
  && post.author.id === currentViewerID.value;

const saveCurrentScroll = (targetUserID: number) => {
  if (typeof window !== 'undefined') {
    profileStore.setScrollY(targetUserID, window.scrollY);
  }
};

const restoreScrollOnce = async () => {
  const entryVersion = profileEntryVersion;
  if (restoredEntryVersion === entryVersion) return;

  const session = activeSession.value;
  const targetUserID = numericUserID.value;
  if (
    !session
    || targetUserID === null
    || !session.profileLoaded
    || (!session.articlesLoaded && session.articlesInitialLoading)
    || typeof window === 'undefined'
  ) return;

  await nextTick();
  if (
    entryVersion !== profileEntryVersion
    || targetUserID !== numericUserID.value
    || restoredEntryVersion === entryVersion
  ) return;

  if (
    typeof window.scrollTo === 'function'
    && !window.navigator.userAgent.toLowerCase().includes('jsdom')
  ) {
    window.scrollTo({ top: session.scrollY, behavior: 'auto' });
  }
  restoredEntryVersion = entryVersion;
};

const retryFollowState = () => {
  if (numericUserID.value !== null && currentViewerID.value !== null) {
    void profileStore.loadFollowState(numericUserID.value, true);
  }
};

const handleFollowToggle = () => {
  if (numericUserID.value !== null) {
    void profileStore.toggleFollow(numericUserID.value);
  }
};

type ProfileEditSnapshot = Pick<PublicUser, 'display_name' | 'bio' | 'avatar_url'>;

const editDialogRef = ref<HTMLDialogElement | null>(null);
const editDisplayNameInputRef = ref<HTMLInputElement | null>(null);
const profileAvatarInputRef = ref<HTMLInputElement | null>(null);
const editOriginal = ref<ProfileEditSnapshot | null>(null);
const editDraft = reactive<ProfileEditSnapshot>({ display_name: '', bio: '', avatar_url: '' });
const pendingAvatarFile = ref<File | null>(null);
const pendingAvatarPreviewURL = ref('');
const editAvatarLoadFailed = ref(false);
const editAvatarError = ref('');
const editError = ref('');
const editSaving = ref(false);

const editDisplayNameLength = computed(() => Array.from(editDraft.display_name.trim()).length);
const editBioLength = computed(() => Array.from(editDraft.bio.trim()).length);
const editDisplayNameOverLimit = computed(() => editDisplayNameLength.value > profileDisplayNameLimit);
const editBioOverLimit = computed(() => editBioLength.value > profileBioLimit);
const editProfileDisplayName = computed(() =>
  editDraft.display_name.trim() || user.value?.username || 'Profile',
);
const editProfileInitial = computed(
  () => Array.from(editProfileDisplayName.value.trim())[0]?.toUpperCase() || '?',
);
const editAvatarPreview = computed(() => {
  if (editAvatarLoadFailed.value) return '';
  return pendingAvatarPreviewURL.value || editDraft.avatar_url;
});
const editAvatarHasValue = computed(() => Boolean(pendingAvatarFile.value || editDraft.avatar_url));
const editCanSave = computed(() => {
  const original = editOriginal.value;
  if (!original || editSaving.value || editDisplayNameOverLimit.value || editBioOverLimit.value) {
    return false;
  }
  return Boolean(
    pendingAvatarFile.value
    || editDraft.display_name.trim() !== original.display_name
    || editDraft.bio.trim() !== original.bio
    || editDraft.avatar_url !== original.avatar_url,
  );
});

const revokePendingAvatarPreview = () => {
  if (pendingAvatarPreviewURL.value) {
    URL.revokeObjectURL(pendingAvatarPreviewURL.value);
    pendingAvatarPreviewURL.value = '';
  }
};

const clearEditDraft = () => {
  revokePendingAvatarPreview();
  pendingAvatarFile.value = null;
  editOriginal.value = null;
  editDraft.display_name = '';
  editDraft.bio = '';
  editDraft.avatar_url = '';
  editAvatarLoadFailed.value = false;
  editAvatarError.value = '';
  editError.value = '';
  if (profileAvatarInputRef.value) {
    profileAvatarInputRef.value.value = '';
  }
};

const forceCloseEditProfile = () => {
  editSaving.value = false;
  clearEditDraft();
  if (editDialogRef.value?.open) {
    editDialogRef.value.close();
  }
};

const openEditProfile = () => {
  if (!user.value || !isOwnProfile.value || editSaving.value) return;
  clearEditDraft();
  editOriginal.value = {
    display_name: user.value.display_name,
    bio: user.value.bio,
    avatar_url: user.value.avatar_url,
  };
  editDraft.display_name = user.value.display_name;
  editDraft.bio = user.value.bio;
  editDraft.avatar_url = user.value.avatar_url;
  editDialogRef.value?.showModal();
  void nextTick(() => editDisplayNameInputRef.value?.focus());
};

const closeEditProfile = () => {
  if (editSaving.value) return;
  clearEditDraft();
  if (editDialogRef.value?.open) editDialogRef.value.close();
};

const handleDialogCancel = (event: Event) => {
  if (editSaving.value) {
    event.preventDefault();
    return;
  }
  clearEditDraft();
};

const handleDialogClose = () => {
  if (!editSaving.value) clearEditDraft();
};

const handleAvatarSelection = (event: Event) => {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file) return;
  if (file.size <= 0 || file.size > profileAvatarMaxBytes) {
    editAvatarError.value = 'Photo must be between 1 byte and 2 MiB.';
    return;
  }
  if (!profileAvatarTypes.has(file.type)) {
    editAvatarError.value = 'Use a JPEG, PNG, or WebP image.';
    return;
  }

  revokePendingAvatarPreview();
  pendingAvatarFile.value = file;
  pendingAvatarPreviewURL.value = URL.createObjectURL(file);
  editAvatarLoadFailed.value = false;
  editAvatarError.value = '';
  editError.value = '';
};

const removeProfileAvatar = () => {
  if (editSaving.value) return;
  revokePendingAvatarPreview();
  pendingAvatarFile.value = null;
  editDraft.avatar_url = '';
  editAvatarLoadFailed.value = false;
  editAvatarError.value = '';
  editError.value = '';
};

const isCurrentEditSession = (
  capture: ProfileSessionCapture,
  profileID: number,
  viewerID: number,
) =>
  editDialogRef.value?.open === true
  && user.value?.id === profileID
  && userId.value === String(profileID)
  && currentViewerID.value === viewerID
  && authStore.isAuthenticated
  && profileStore.isCurrentSessionCapture(capture);

const buildProfilePatch = (): UpdateUserProfilePayload => {
  const original = editOriginal.value;
  if (!original) return {};
  const payload: UpdateUserProfilePayload = {};
  const displayName = editDraft.display_name.trim();
  const bio = editDraft.bio.trim();
  if (displayName !== original.display_name) payload.display_name = displayName;
  if (bio !== original.bio) payload.bio = bio;
  if (editDraft.avatar_url !== original.avatar_url) payload.avatar_url = editDraft.avatar_url;
  return payload;
};

const profileEditErrorMessage = (error: unknown, action: 'upload' | 'save') => {
  const status = getErrorStatus(error);
  if (status === 401) return 'Please log in again and retry.';
  if (status === 400) {
    return action === 'upload'
      ? 'That photo could not be uploaded.'
      : 'Check your profile fields and retry.';
  }
  return action === 'upload'
    ? 'Could not upload photo. Please retry.'
    : 'Could not save profile. Please retry.';
};

const saveProfile = async () => {
  const profile = user.value;
  const viewerID = currentViewerID.value;
  if (!profile || viewerID === null || !editCanSave.value) return;

  const capture = profileStore.captureSession(profile.id);
  if (!capture) return;
  const selectedFile = pendingAvatarFile.value;
  editSaving.value = true;
  editError.value = '';

  try {
    if (selectedFile) {
      const uploadedAvatarURL = await uploadProfileAvatar(selectedFile);
      if (!isCurrentEditSession(capture, profile.id, viewerID)) return;
      editDraft.avatar_url = uploadedAvatarURL;
      pendingAvatarFile.value = null;
      revokePendingAvatarPreview();
      editAvatarLoadFailed.value = false;
    }

    const payload = buildProfilePatch();
    if (Object.keys(payload).length === 0) {
      editSaving.value = false;
      closeEditProfile();
      return;
    }

    const updatedUser = await updateUserProfile(profile.id, payload);
    if (!isCurrentEditSession(capture, profile.id, viewerID)) return;
    profileStore.updateUser(updatedUser);
    authStore.syncCurrentIdentityProfile(updatedUser);
    editSaving.value = false;
    clearEditDraft();
    editDialogRef.value?.close();
  } catch (error) {
    if (isCurrentEditSession(capture, profile.id, viewerID)) {
      editError.value = profileEditErrorMessage(
        error,
        selectedFile && pendingAvatarFile.value ? 'upload' : 'save',
      );
    }
  } finally {
    if (isCurrentEditSession(capture, profile.id, viewerID)) {
      editSaving.value = false;
    }
  }
};

const loadProfile = (force = false) => {
  invalidProfileError.value = '';
  if (numericUserID.value === null) {
    invalidProfileError.value = 'This profile URL is not valid.';
    return;
  }
  forceCloseEditProfile();
  void profileStore.loadProfile(numericUserID.value, force);
};

const retryProfile = () => {
  loadProfile(true);
};

const retryInitialPosts = () => {
  if (numericUserID.value !== null) {
    void profileStore.loadArticles(numericUserID.value, true);
  }
};

const loadMorePosts = () => {
  if (numericUserID.value !== null) {
    void profileStore.loadMoreArticles(numericUserID.value);
  }
};

const retryLoadMore = () => {
  if (numericUserID.value !== null) {
    profileStore.retryLoadMoreArticles(numericUserID.value);
  }
};

const handleDeletePost = async (articleId: number) => {
  const removed = await profileStore.deletePost(articleId, numericUserID.value ?? undefined);
  if (removed) {
    disconnectObserver();
    await nextTick(updateObserver);
  }
};

const handleLikeToggle = (articleId: number) => {
  void profileStore.toggleLike(articleId, numericUserID.value ?? undefined);
};

const handleRepostToggle = (articleId: number) => {
  void profileStore.toggleRepost(articleId, numericUserID.value ?? undefined);
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

const disconnectObserver = () => {
  observer?.disconnect();
  observer = null;
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
      loadMorePosts();
    }
  }, { rootMargin: '240px 0px' });
  observer.observe(sentinelRef.value);
};

watch(userId, (nextID, previousID) => {
  profileEntryVersion += 1;
  const previousNumericID = Number(previousID);
  if (Number.isSafeInteger(previousNumericID) && previousNumericID > 0) {
    saveCurrentScroll(previousNumericID);
    profileStore.cancelPendingDeletesForProfile(previousNumericID);
  }
  invalidProfileError.value = '';
  loadProfile();
}, { immediate: true });

watch(currentViewerID, (nextViewerID, previousViewerID) => {
  if (nextViewerID === previousViewerID) return;
  profileEntryVersion += 1;
  forceCloseEditProfile();
  loadProfile();
});

watch(
  [
    userId,
    () => activeSession.value?.profileLoaded,
    () => activeSession.value?.articlesLoaded,
    () => activeSession.value?.articlesInitialLoading,
    () => activeSession.value?.articlesInitialError,
  ],
  () => { void restoreScrollOnce(); },
  { flush: 'post', immediate: true },
);

watch(
  [
    userId,
    () => activeSession.value?.profileLoaded,
    () => activeSession.value?.articlesLoaded,
    () => activeSession.value?.hasMore,
    () => activeSession.value?.articlesLoadingMore,
    () => activeSession.value?.articlesLoadMoreError,
    () => activeSession.value?.articles.length,
  ],
  () => { void nextTick(updateObserver); },
  { flush: 'post' },
);

onMounted(() => {
  void nextTick(updateObserver);
});

onBeforeUnmount(() => {
  if (numericUserID.value !== null) saveCurrentScroll(numericUserID.value);
  forceCloseEditProfile();
  disconnectObserver();
});
</script>

<style scoped>
.profile-view {
  min-height: 100vh;
  min-height: 100dvh;
  color: var(--color-text);
  background: var(--color-surface);
}

.profile-header {
  position: sticky;
  top: 0;
  z-index: 12;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  min-height: 56px;
  padding: var(--space-2) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.profile-header__back {
  display: inline-flex;
  align-items: center;
  flex: 1 1 auto;
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

.profile-header__back .app-icon {
  flex: 0 0 auto;
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
  position: relative;
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

.profile-social a {
  color: inherit;
  text-decoration: none;
}

.profile-social a:hover,
.profile-social a:focus-visible {
  color: var(--color-accent);
  text-decoration: underline;
  text-underline-offset: 3px;
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
    top: var(--mobile-safe-top);
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
.profile-avatar img {
  position: absolute;
  inset: 0;
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.profile-identity__bio {
  margin: var(--space-3) 0 0;
  color: var(--color-text);
  line-height: 1.45;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.profile-action--primary {
  border-color: var(--color-text);
  background: var(--color-text);
  color: var(--color-surface);
}

.profile-action--primary:hover,
.profile-action--primary:focus-visible {
  border-color: var(--color-accent);
  background: var(--color-accent);
  color: #fff;
}

.profile-edit-dialog {
  width: min(calc(100% - 32px), 520px);
  max-height: calc(100vh - 32px);
  margin: auto;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  padding: 0;
  background: var(--color-surface);
  color: var(--color-text);
}

.profile-edit-dialog::backdrop {
  background: rgba(15, 20, 25, 0.62);
}

.profile-edit-form {
  display: grid;
  gap: var(--space-5);
  max-height: calc(100vh - 32px);
  overflow-y: auto;
  padding: var(--space-6);
}

.profile-edit-dialog__header,
.profile-edit-dialog__actions,
.profile-edit-field__label-row,
.profile-edit-avatar__actions {
  display: flex;
  align-items: center;
}

.profile-edit-dialog__header,
.profile-edit-dialog__actions,
.profile-edit-field__label-row {
  justify-content: space-between;
}

.profile-edit-dialog__header h2 {
  margin: 0;
  font-size: 22px;
  letter-spacing: -0.02em;
}

.profile-edit-dialog__close {
  display: inline-grid;
  width: 40px;
  height: 40px;
  place-items: center;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
  font-size: 24px;
  line-height: 1;
}

.profile-edit-dialog__close:hover,
.profile-edit-dialog__close:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-text);
}

.profile-edit-dialog__close:disabled {
  cursor: wait;
  opacity: 0.55;
}

.profile-edit-avatar {
  display: flex;
  align-items: center;
  gap: var(--space-4);
}

.profile-avatar--edit {
  width: 88px;
  height: 88px;
  flex: 0 0 auto;
  font-size: 30px;
}

.profile-edit-avatar__copy {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
}

.profile-edit-avatar__actions {
  flex-wrap: wrap;
  gap: var(--space-2);
}

.profile-edit-avatar__actions .profile-action {
  gap: var(--space-1);
}

.profile-edit-field {
  display: grid;
  gap: var(--space-2);
}

.profile-edit-field__label,
.profile-edit-field__label-row,
.profile-edit-field--readonly small {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.profile-edit-field__label-row {
  gap: var(--space-3);
  font-weight: 700;
}

.profile-edit-field__label-row span {
  color: var(--color-text-tertiary);
  font-size: 12px;
  font-weight: 500;
}

.profile-edit-field input:not([type="file"]),
.profile-edit-field textarea {
  width: 100%;
  box-sizing: border-box;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-sm);
  padding: var(--space-3);
  background: var(--color-surface-subtle);
  color: var(--color-text);
  font: inherit;
  font-size: 15px;
}

.profile-edit-field textarea {
  min-height: 104px;
  resize: vertical;
}

.profile-edit-field input:not([type="file"]):focus,
.profile-edit-field textarea:focus {
  border-color: var(--color-accent);
  outline: 2px solid color-mix(in srgb, var(--color-accent) 24%, transparent);
  outline-offset: 1px;
}

.profile-edit-field--readonly {
  align-items: start;
  padding: var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  background: var(--color-surface-subtle);
}

.profile-edit-field--readonly strong {
  font-size: 15px;
}

.profile-edit-error {
  margin: 0;
  color: var(--color-danger);
  font-size: 13px;
  line-height: 1.4;
}

.profile-edit-dialog__actions {
  gap: var(--space-3);
  padding-top: var(--space-2);
}

.profile-edit-dialog__actions > button {
  min-width: 92px;
}

.profile-edit-dialog [disabled] {
  cursor: wait;
  opacity: 0.62;
}

@media (max-width: 520px) {
  .profile-edit-form {
    padding: var(--space-5) var(--space-4);
  }

  .profile-edit-avatar {
    align-items: flex-start;
  }
}
</style>
