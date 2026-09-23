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

    <div
      ref="profileScrollViewportRef"
      class="profile-scroll-viewport"
    >
    <section
      v-if="invalidProfileError"
      class="profile-state profile-state--page"
      aria-live="polite"
      role="alert"
    >
      <h1>Profile could not be loaded.</h1>
      <p>{{ invalidProfileError }}</p>
      <div class="profile-state__actions">
        <button class="profile-action" type="button" @click="retryProfile">
          Retry
        </button>
      </div>
    </section>

    <section
      v-else-if="profileLoading"
      class="profile-identity profile-identity--loading"
      aria-live="polite"
      aria-label="Loading profile"
    >
      <div class="profile-cover profile-skeleton profile-skeleton--cover" aria-hidden="true"></div>
      <div class="profile-identity__body">
        <div class="profile-identity__top-row">
          <span class="profile-skeleton profile-skeleton--avatar" aria-hidden="true"></span>
        </div>
        <div class="profile-identity__copy" aria-hidden="true">
          <span class="profile-skeleton profile-skeleton--name"></span>
          <span class="profile-skeleton profile-skeleton--handle"></span>
        </div>
      </div>
    </section>

    <section
      v-else-if="user && canRenderProfile"
      class="profile-identity"
      aria-labelledby="profile-name"
    >
      <div class="profile-cover" aria-hidden="true">
        <img
          v-if="profileCoverURL && !coverLoadFailed"
          class="profile-cover__image"
          :src="profileCoverURL"
          alt=""
          loading="eager"
          decoding="async"
          fetchpriority="high"
          @error="handleCoverError"
        />
      </div>

      <div class="profile-identity__body">
        <div class="profile-identity__top-row">
          <UserAvatar
            class="profile-avatar"
            :avatar-url="user.avatar_url"
            :display-name="user.display_name"
            :username="user.username"
            :size="112"
            loading="eager"
            decorative
          />
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
                :aria-pressed="authStore.isAuthenticated ? followState?.following === true : undefined"
                :aria-busy="followPending"
                :disabled="authStore.isAuthenticated && followPending"
                @click="handleFollowToggle"
              >
                {{ authStore.isAuthenticated && followState?.following ? 'Following' : 'Follow' }}
              </button>
              <p v-if="followActionError" class="profile-action-error" aria-live="polite">
                {{ followActionError }}
              </p>
            </template>
          </div>
        </div>

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
          <div class="profile-social" aria-label="Social stats">
            <RouterLink :to="{ name: 'UserFollowing', params: { id: user.id } }">
              <span class="profile-social__item"><strong>{{ displayedFollowingCount }}</strong> Following</span>
            </RouterLink>
            <RouterLink :to="{ name: 'UserFollowers', params: { id: user.id } }">
              <span class="profile-social__item"><strong>{{ displayedFollowerCount }}</strong> Followers</span>
            </RouterLink>
          </div>
          <p v-if="followError" class="profile-social-error" aria-live="polite">
            <span>Follow status unavailable.</span>
            <button class="profile-action profile-action--compact" type="button" @click="retryFollowState">
              Retry
            </button>
          </p>
        </div>
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

    <template v-if="user && canRenderProfile">
      <p v-if="bookmarkMutationError" class="profile-action-error" role="status" aria-live="polite">
        {{ bookmarkMutationError }}
      </p>

      <section class="profile-posts" aria-labelledby="profile-posts-heading">
        <h2 id="profile-posts-heading" class="sr-only">Posts</h2>

        <div
          v-if="timelineInitialLoading"
          class="profile-skeleton-list"
          aria-live="polite"
          aria-label="Loading timeline"
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
          v-else-if="timelineInitialError"
          class="profile-state profile-state--inline"
          role="alert"
        >
          <p>Timeline could not be loaded.</p>
          <button class="profile-action" type="button" @click="retryInitialTimeline">
            Retry timeline
          </button>
        </div>

        <p v-else-if="timelineItems.length === 0" class="profile-empty">
          No posts or reposts yet.
        </p>

        <div v-else class="profile-post-list">
          <PostCard
            v-for="item in timelineItems"
            :key="`${item.activityType}:${item.sourceId}`"
            :post="item.post"
            :like-pending="likePendingPostIds.has(item.post.id)"
            :repost-pending="repostPendingPostIds.has(item.post.id)"
            :bookmark-pending="bookmarkPendingPostIds.has(item.post.id)"
            :track-view="authStore.isAuthenticated"
            :requires-auth-for-actions="!authStore.isAuthenticated"
            :show-delete="canDeletePost(item.post)"
            :delete-pending="pendingDeletePostIds.has(item.post.id)"
            :delete-error="deleteErrors.get(item.post.id) || ''"
            @toggle-like="handleLikeToggle"
            @toggle-repost="handleRepostToggle"
            @toggle-bookmark="handleBookmarkToggle"
            @delete-post="handleDeletePost"
          />
        </div>

        <div
          v-if="hasMore || timelineLoadingMore || timelineLoadMoreError"
          ref="sentinelRef"
          class="profile-feed-sentinel"
          aria-live="polite"
        >
          <span v-if="timelineLoadingMore">Loading more activity...</span>

          <template v-else-if="timelineLoadMoreError">
            <span>Could not load more activity.</span>
            <button class="profile-action" type="button" @click="retryLoadMore">
              Retry
            </button>
          </template>

          <button
            v-else-if="!intersectionObserverAvailable && hasMore"
            class="profile-action"
            type="button"
            @click="loadMoreTimeline"
          >
            Load more activity
          </button>
        </div>
      </section>
    </template>
    </div>
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
            @click="requestCloseEditProfile"
          >
            <AppIcon name="close" :size="20" />
          </button>
        </header>

        <div class="profile-edit-cover">
          <div class="profile-edit-cover__preview">
            <img
              v-if="editCoverPreview"
              :src="editCoverPreview"
              alt="Profile cover preview"
              @error="editCoverLoadFailed = true"
            />
            <input
              id="profile-cover-input"
              ref="profileCoverInputRef"
              class="sr-only"
              type="file"
              tabindex="-1"
              accept="image/jpeg,image/png,image/webp"
              :disabled="editSaving"
              @change="handleCoverSelection"
            />
            <button
              ref="profileCoverChangeButtonRef"
              class="profile-edit-media-change profile-edit-cover__change"
              type="button"
              aria-label="Change cover"
              :disabled="editSaving"
              @click="openCoverFilePicker"
            >
              <AppIcon name="camera" :size="20" />
            </button>
          </div>
          <div class="profile-edit-cover__actions">
            <button
              class="profile-edit-media-remove"
              type="button"
              :disabled="editSaving || !editCoverHasValue"
              @click="removeProfileCover"
            >
              Remove cover
            </button>
          </div>
          <p v-if="editCoverError" class="profile-edit-error" role="alert">{{ editCoverError }}</p>
        </div>

        <div class="profile-edit-avatar">
          <div class="profile-avatar profile-avatar--edit">
            <img
              v-if="editAvatarPreview"
              :src="editAvatarPreview"
              :alt="editProfileDisplayName + ' avatar preview'"
              @error="editAvatarLoadFailed = true"
            />
            <span v-else aria-hidden="true">{{ editProfileInitial }}</span>
            <input
              id="profile-avatar-input"
              ref="profileAvatarInputRef"
              class="sr-only"
              type="file"
              tabindex="-1"
              accept="image/jpeg,image/png,image/webp"
              :disabled="editSaving"
              @change="handleAvatarSelection"
            />
            <button
              ref="profileAvatarChangeButtonRef"
              class="profile-edit-media-change profile-edit-avatar__change"
              type="button"
              aria-label="Change photo"
              :disabled="editSaving"
              @click="openAvatarFilePicker"
            >
              <AppIcon name="camera" :size="20" />
            </button>
          </div>
          <div class="profile-edit-avatar__copy">
            <div class="profile-edit-avatar__actions">
              <button
                class="profile-edit-media-remove"
                type="button"
                :disabled="editSaving || !editAvatarHasValue"
                @click="removeProfileAvatar"
              >
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
          <button class="profile-action" type="button" :disabled="editSaving" @click="requestCloseEditProfile">
            Cancel
          </button>
          <button class="profile-follow-button" type="submit" :disabled="!editCanSave" :aria-busy="editSaving">
            {{ editSaving ? 'Saving…' : 'Save' }}
          </button>
        </footer>
      </form>
    </dialog>

    <AvatarCropDialog
      v-if="avatarCropOpen && avatarCropSourceFile"
      :file="avatarCropSourceFile"
      @cancel="handleAvatarCropCancel"
      @apply="handleAvatarCropApply"
    />

    <CoverCropDialog
      v-if="coverCropOpen && coverCropSourceFile"
      :file="coverCropSourceFile"
      @cancel="handleCoverCropCancel"
      @apply="handleCoverCropApply"
    />

    <ConfirmDialog
      v-if="discardConfirmOpen"
      title="Discard changes?"
      description="Your profile changes haven't been saved."
      confirm-label="Discard"
      cancel-label="Keep editing"
      danger
      @confirm="confirmDiscardEdit"
      @cancel="cancelDiscardEdit"
    />
</template>

<script setup lang="ts">
import {
  computed,
  nextTick,
  onActivated,
  onBeforeUnmount,
  onDeactivated,
  onMounted,
  reactive,
  ref,
  watch,
} from 'vue';
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router';
import ConfirmDialog from '../components/dialogs/ConfirmDialog.vue';
import PostCard from '../components/feed/PostCard.vue';
import AppIcon from '../components/icons/AppIcon.vue';
import MobileAccountMenu from '../components/layout/MobileAccountMenu.vue';
import AvatarCropDialog from '../components/profile/AvatarCropDialog.vue';
import CoverCropDialog from '../components/profile/CoverCropDialog.vue';
import UserAvatar from '../components/users/UserAvatar.vue';
import { usePageTitle } from '../composables/usePageTitle';
import { updateUserProfile, uploadProfileAvatar, uploadProfileCover } from '../services/userService';
import type { UpdateUserProfilePayload, UserFollowState } from '../services/userService';
import { useAuthStore } from '../store/auth';
import { useProfileSessionStore, type ProfileSessionCapture } from '../store/profileSession';
import type { PublicUser } from '../types/User';

defineOptions({
  name: 'UserProfileView',
});

const profileDisplayNameLimit = 50;
const profileBioLimit = 160;
const profileAvatarSourceMaxBytes = 10 * 1024 * 1024;
const profileAvatarGeneratedMaxBytes = 2 * 1024 * 1024;
const profileAvatarTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);
const profileCoverMaxBytes = 5 * 1024 * 1024;
const profileCoverTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);
const profileCoverGeneratedTypes = new Set(['image/jpeg', 'image/png']);
const skeletonCount = 3;

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const profileStore = useProfileSessionStore();

const readProfileRouteID = () => (
  route.name === 'UserProfile'
    ? String(route.params.id ?? '').trim()
    : ''
);

const profileRouteID = ref(readProfileRouteID());
const profileViewActive = ref(true);
let resumeOnActivation = false;
const bookmarkMutationError = ref('');

const userId = computed(() => profileRouteID.value);
const numericUserID = computed(() => {
  const value = Number(userId.value);
  return Number.isSafeInteger(value) && value > 0 ? value : null;
});
const activeSession = computed(() => (
  numericUserID.value === null ? null : profileStore.ensureSession(numericUserID.value)
));
const invalidProfileError = ref('');

const syncProfileRouteID = () => {
  if (!profileViewActive.value || route.name !== 'UserProfile') {
    return;
  }

  const nextID = String(route.params.id ?? '').trim();
  if (profileRouteID.value === nextID) {
    return;
  }
  profileRouteID.value = nextID;
};

const user = computed(() => activeSession.value?.user ?? null);
const profileLoading = computed(() => activeSession.value?.profileLoading ?? false);
const profileError = computed(() => invalidProfileError.value || activeSession.value?.profileError || '');
const profileNotFound = computed(() => activeSession.value?.profileNotFound ?? false);
const timelineItems = computed(() => activeSession.value?.timelineItems ?? []);
const timelineInitialLoading = computed(() => activeSession.value?.timelineInitialLoading ?? false);
const timelineLoadingMore = computed(() => activeSession.value?.timelineLoadingMore ?? false);
const timelineInitialError = computed(() => activeSession.value?.timelineInitialError ?? '');
const timelineLoadMoreError = computed(() => activeSession.value?.timelineLoadMoreError ?? '');
const nextCursor = computed(() => activeSession.value?.nextCursor ?? null);
const hasMore = computed(() => activeSession.value?.hasMore ?? false);
const followState = computed<UserFollowState | null>(() => activeSession.value?.followState ?? null);
const followLoading = computed(() => activeSession.value?.followLoading ?? false);
const followError = computed(() => activeSession.value?.followError ?? '');
const followActionError = computed(() => activeSession.value?.followActionError ?? '');
const followPending = computed(() => activeSession.value?.followPending ?? false);
const displayedFollowerCount = computed(() => (
  followState.value?.follower_count
  ?? user.value?.follower_count
  ?? 0
));
const displayedFollowingCount = computed(() => (
  followState.value?.following_count
  ?? user.value?.following_count
  ?? 0
));

const likePendingPostIds = profileStore.likePendingPostIds;
const repostPendingPostIds = profileStore.repostPendingPostIds;
const bookmarkPendingPostIds = profileStore.bookmarkPendingPostIds;
const pendingDeletePostIds = profileStore.pendingDeletePostIds;
const deleteErrors = profileStore.deleteErrors;

const profileScrollViewportRef = ref<HTMLElement | null>(null);
const sentinelRef = ref<HTMLElement | null>(null);
const intersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
let observer: IntersectionObserver | null = null;
let profileEntryVersion = 0;
let restoredEntryVersion = -1;

const beginProfileRestoreEpoch = () => {
  profileEntryVersion += 1;
  restoredEntryVersion = -1;
};

const currentViewerID = computed(() => {
  if (!authStore.isAuthenticated) return null;
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});
const profileDisplayName = computed(() => {
  const displayName = user.value?.display_name?.trim() ?? '';
  return displayName || user.value?.username || 'Profile';
});
const profileCoverURL = computed(() => user.value?.cover_image_url?.trim() || '');
const coverLoadFailed = ref(false);
const headerUsername = computed(() => (
  user.value
    ? profileDisplayName.value
    : 'Profile'
));
const profilePageTitle = computed(() => {
  const username = user.value?.username?.trim();
  return username ? `@${username}` : 'Profile';
});
usePageTitle(profilePageTitle, profileViewActive);
const joinedLabel = computed(() => {
  const value = user.value?.created_at;
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleDateString(undefined, { month: 'long', year: 'numeric' });
});

const handleCoverError = () => {
  coverLoadFailed.value = true;
};

watch(
  [() => user.value?.id, profileCoverURL],
  () => {
    coverLoadFailed.value = false;
  },
  { immediate: true },
);

const getErrorStatus = (error: unknown) =>
  (error as { response?: { status?: number } }).response?.status;

const canRenderProfile = computed(() => Boolean(user.value));
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
  user.value
  && (currentViewerID.value === null || user.value.id !== currentViewerID.value)
  && (!authStore.isAuthenticated || socialReady.value),
));
const canDeletePost = (post: { author: { id: number } }) =>
  authStore.isAuthenticated
  && currentViewerID.value !== null
  && post.author.id === currentViewerID.value;

const saveCurrentScroll = (targetUserID: number) => {
  const viewport = profileScrollViewportRef.value;
  if (!viewport) {
    return;
  }

  profileStore.setScrollTop(targetUserID, viewport.scrollTop);
};

const restoreScrollOnce = async () => {
  const entryVersion = profileEntryVersion;
  if (!profileViewActive.value || restoredEntryVersion === entryVersion) return;

  const session = activeSession.value;
  const targetUserID = numericUserID.value;
  if (
    !session
    || targetUserID === null
    || !session.profileLoaded
    || (!session.timelineLoaded && session.timelineInitialLoading)
  ) return;

  await nextTick();
  if (
    !profileViewActive.value
    || entryVersion !== profileEntryVersion
    || targetUserID !== numericUserID.value
    || restoredEntryVersion === entryVersion
  ) return;

  const viewport = profileScrollViewportRef.value;
  if (!viewport) {
    return;
  }

  viewport.scrollTop = session.scrollTop;
  restoredEntryVersion = entryVersion;
};

onBeforeRouteLeave(() => {
  const profileID = numericUserID.value;
  if (profileID !== null) {
    saveCurrentScroll(profileID);
  }
});

const retryFollowState = () => {
  if (numericUserID.value !== null && currentViewerID.value !== null) {
    void profileStore.loadFollowState(numericUserID.value, true);
  }
};

const navigateToLogin = () => {
  void router.push({
    name: 'Login',
    query: { returnTo: route.fullPath },
  });
};

const handleFollowToggle = () => {
  if (!authStore.isAuthenticated || currentViewerID.value === null) {
    navigateToLogin();
    return;
  }
  if (numericUserID.value !== null) {
    void profileStore.toggleFollow(numericUserID.value);
  }
};

type ProfileEditSnapshot = Pick<PublicUser, 'display_name' | 'bio' | 'avatar_url' | 'cover_image_url'>;
type ProfileSavePhase = 'avatar-upload' | 'cover-upload' | 'profile-save';

const editDialogRef = ref<HTMLDialogElement | null>(null);
const editDisplayNameInputRef = ref<HTMLInputElement | null>(null);
const profileAvatarInputRef = ref<HTMLInputElement | null>(null);
const profileCoverInputRef = ref<HTMLInputElement | null>(null);
const profileAvatarChangeButtonRef = ref<HTMLButtonElement | null>(null);
const profileCoverChangeButtonRef = ref<HTMLButtonElement | null>(null);
const editOriginal = ref<ProfileEditSnapshot | null>(null);
const editDraft = reactive<ProfileEditSnapshot>({
  display_name: '',
  bio: '',
  avatar_url: '',
  cover_image_url: '',
});
const pendingAvatarFile = ref<File | null>(null);
const pendingAvatarPreviewURL = ref('');
const avatarCropOpen = ref(false);
const avatarCropSourceFile = ref<File | null>(null);
const pendingCoverFile = ref<File | null>(null);
const pendingCoverPreviewURL = ref('');
const coverCropOpen = ref(false);
const coverCropSourceFile = ref<File | null>(null);
const editAvatarLoadFailed = ref(false);
const editAvatarError = ref('');
const editCoverLoadFailed = ref(false);
const editCoverError = ref('');
const editError = ref('');
const editSaving = ref(false);
const discardConfirmOpen = ref(false);
const discardFocusTarget = ref<HTMLElement | null>(null);
const discardFocusSelector = ref('');

const restoreProfileMediaChangeFocus = (target: HTMLButtonElement | null) => {
  void nextTick(() => {
    if (
      editDialogRef.value?.open !== true
      || !target?.isConnected
      || target.disabled
    ) {
      return;
    }

    target.focus();
  });
};

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
const editCoverPreview = computed(() => {
  if (editCoverLoadFailed.value) return '';
  return pendingCoverPreviewURL.value || editDraft.cover_image_url;
});
const editCoverHasValue = computed(() => Boolean(pendingCoverFile.value || editDraft.cover_image_url));
const editDirty = computed(() => {
  const original = editOriginal.value;
  if (!original) return false;

  return Boolean(
    pendingAvatarFile.value
    || pendingCoverFile.value
    || editDraft.display_name.trim() !== original.display_name
    || editDraft.bio.trim() !== original.bio
    || editDraft.avatar_url !== original.avatar_url
    || editDraft.cover_image_url !== original.cover_image_url,
  );
});
const editCanSave = computed(() => (
  editDirty.value
  && !editSaving.value
  && !editDisplayNameOverLimit.value
  && !editBioOverLimit.value
));

const revokePendingAvatarPreview = () => {
  if (pendingAvatarPreviewURL.value) {
    URL.revokeObjectURL(pendingAvatarPreviewURL.value);
    pendingAvatarPreviewURL.value = '';
  }
};

const revokePendingCoverPreview = () => {
  if (pendingCoverPreviewURL.value) {
    URL.revokeObjectURL(pendingCoverPreviewURL.value);
    pendingCoverPreviewURL.value = '';
  }
};

const clearEditDraft = () => {
  revokePendingAvatarPreview();
  revokePendingCoverPreview();
  pendingAvatarFile.value = null;
  pendingCoverFile.value = null;
  avatarCropOpen.value = false;
  avatarCropSourceFile.value = null;
  coverCropOpen.value = false;
  coverCropSourceFile.value = null;
  editOriginal.value = null;
  editDraft.display_name = '';
  editDraft.bio = '';
  editDraft.avatar_url = '';
  editDraft.cover_image_url = '';
  editAvatarLoadFailed.value = false;
  editAvatarError.value = '';
  editCoverLoadFailed.value = false;
  editCoverError.value = '';
  editError.value = '';
  if (profileAvatarInputRef.value) {
    profileAvatarInputRef.value.value = '';
  }
  if (profileCoverInputRef.value) {
    profileCoverInputRef.value.value = '';
  }
};

const forceCloseEditProfile = () => {
  editSaving.value = false;
  discardConfirmOpen.value = false;
  discardFocusTarget.value = null;
  discardFocusSelector.value = '';
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
    cover_image_url: user.value.cover_image_url,
  };
  editDraft.display_name = user.value.display_name;
  editDraft.bio = user.value.bio;
  editDraft.avatar_url = user.value.avatar_url;
  editDraft.cover_image_url = user.value.cover_image_url;
  editDialogRef.value?.showModal();
};

const requestCloseEditProfile = (event?: Event) => {
  if (editSaving.value) return;

  if (editDirty.value) {
    if (discardConfirmOpen.value) return;
    const currentTarget = event?.currentTarget;
    const activeElement = document.activeElement;
    if (currentTarget instanceof HTMLButtonElement && editDialogRef.value?.contains(currentTarget)) {
      discardFocusTarget.value = currentTarget;
      discardFocusSelector.value = currentTarget.classList.contains('profile-edit-dialog__close')
        ? '.profile-edit-dialog__close'
        : currentTarget.id
          ? `#${currentTarget.id}`
          : '';
    } else if (activeElement instanceof HTMLElement && editDialogRef.value?.contains(activeElement)) {
      discardFocusTarget.value = activeElement;
      discardFocusSelector.value = activeElement.id ? `#${activeElement.id}` : '';
    } else {
      discardFocusTarget.value = null;
      discardFocusSelector.value = '';
    }
    if (!discardFocusTarget.value) {
      discardFocusTarget.value = editDisplayNameInputRef.value;
      discardFocusSelector.value = '#profile-display-name';
    }
    discardConfirmOpen.value = true;
    return;
  }

  forceCloseEditProfile();
};

const handleDialogCancel = (event: Event) => {
  event.preventDefault();
  if (editSaving.value) {
    return;
  }
  requestCloseEditProfile(event);
};

const handleDialogClose = () => {
  discardConfirmOpen.value = false;
  if (!editSaving.value) clearEditDraft();
};

const confirmDiscardEdit = () => {
  forceCloseEditProfile();
};

const cancelDiscardEdit = () => {
  discardConfirmOpen.value = false;
  const focusTarget = discardFocusTarget.value;
  const focusSelector = discardFocusSelector.value;
  discardFocusTarget.value = null;
  discardFocusSelector.value = '';
  void nextTick(() => {
    void nextTick(() => {
      const currentFocusTarget = focusTarget?.isConnected
        ? focusTarget
        : focusSelector
          ? editDialogRef.value?.querySelector<HTMLElement>(focusSelector)
          : null;
      if (currentFocusTarget) {
        currentFocusTarget.focus();
        return;
      }
      editDisplayNameInputRef.value?.focus();
    });
  });
};

const openCoverFilePicker = () => {
  if (editSaving.value) return;
  profileCoverInputRef.value?.click();
};

const openAvatarFilePicker = () => {
  if (editSaving.value) return;
  profileAvatarInputRef.value?.click();
};

const handleAvatarSelection = (event: Event) => {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file) return;
  if (file.size <= 0) {
    editAvatarError.value = 'Photo must be between 1 byte and 10 MB.';
    return;
  }
  if (file.size > profileAvatarSourceMaxBytes) {
    editAvatarError.value = 'Photo is too large. Choose an image under 10 MB.';
    return;
  }
  if (!profileAvatarTypes.has(file.type)) {
    editAvatarError.value = 'Use a JPEG, PNG, or WebP image.';
    return;
  }

  avatarCropSourceFile.value = file;
  avatarCropOpen.value = true;
  editAvatarError.value = '';
  editError.value = '';
};

const handleAvatarCropCancel = () => {
  avatarCropOpen.value = false;
  avatarCropSourceFile.value = null;
  restoreProfileMediaChangeFocus(profileAvatarChangeButtonRef.value);
};

const handleAvatarCropApply = (file: File) => {
  if (file.size <= 0 || file.size > profileAvatarGeneratedMaxBytes) {
    editAvatarError.value = 'Could not prepare this photo. Try another image.';
    return;
  }

  revokePendingAvatarPreview();
  pendingAvatarFile.value = file;
  pendingAvatarPreviewURL.value = URL.createObjectURL(file);
  editAvatarLoadFailed.value = false;
  editAvatarError.value = '';
  editError.value = '';
  avatarCropOpen.value = false;
  avatarCropSourceFile.value = null;
  restoreProfileMediaChangeFocus(profileAvatarChangeButtonRef.value);
};

const handleCoverSelection = (event: Event) => {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file) return;
  if (file.size <= 0 || file.size > profileCoverMaxBytes) {
    editCoverError.value = 'Cover must be between 1 byte and 5 MiB.';
    return;
  }
  if (!profileCoverTypes.has(file.type)) {
    editCoverError.value = 'Use a JPEG, PNG, or WebP image.';
    return;
  }

  coverCropSourceFile.value = file;
  coverCropOpen.value = true;
  editCoverError.value = '';
  editError.value = '';
};

const handleCoverCropCancel = () => {
  coverCropOpen.value = false;
  coverCropSourceFile.value = null;
  restoreProfileMediaChangeFocus(profileCoverChangeButtonRef.value);
};

const handleCoverCropApply = (file: File) => {
  if (
    file.size <= 0
    || file.size > profileCoverMaxBytes
    || !profileCoverGeneratedTypes.has(file.type)
  ) {
    editCoverError.value = 'Could not prepare this cover. Try another image.';
    return;
  }

  revokePendingCoverPreview();
  pendingCoverFile.value = file;
  pendingCoverPreviewURL.value = URL.createObjectURL(file);
  editCoverLoadFailed.value = false;
  editCoverError.value = '';
  editError.value = '';
  coverCropOpen.value = false;
  coverCropSourceFile.value = null;
  restoreProfileMediaChangeFocus(profileCoverChangeButtonRef.value);
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

const removeProfileCover = () => {
  if (editSaving.value) return;
  revokePendingCoverPreview();
  pendingCoverFile.value = null;
  editDraft.cover_image_url = '';
  editCoverLoadFailed.value = false;
  editCoverError.value = '';
  editError.value = '';
  if (profileCoverInputRef.value) {
    profileCoverInputRef.value.value = '';
  }
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
  if (editDraft.cover_image_url !== original.cover_image_url) {
    payload.cover_image_url = editDraft.cover_image_url;
  }
  return payload;
};

const profileEditErrorMessage = (error: unknown, phase: ProfileSavePhase) => {
  const status = getErrorStatus(error);
  if (status === 401) return 'Please log in again and retry.';
  if (status === 400) {
    if (phase === 'avatar-upload') return 'That photo could not be uploaded.';
    if (phase === 'cover-upload') return 'That cover image could not be uploaded.';
    return 'Check your profile fields and retry.';
  }
  if (phase === 'avatar-upload') return 'Could not upload photo. Please retry.';
  if (phase === 'cover-upload') return 'Could not upload cover image. Please retry.';
  return 'Could not save profile. Please retry.';
};

const saveProfile = async () => {
  const profile = user.value;
  const viewerID = currentViewerID.value;
  if (!profile || viewerID === null || !editCanSave.value) return;

  const capture = profileStore.captureSession(profile.id);
  if (!capture) return;
  const selectedAvatarFile = pendingAvatarFile.value;
  let phase: ProfileSavePhase = 'profile-save';
  editSaving.value = true;
  editError.value = '';

  try {
    if (selectedAvatarFile) {
      phase = 'avatar-upload';
      const uploadedAvatarURL = await uploadProfileAvatar(selectedAvatarFile);
      if (!isCurrentEditSession(capture, profile.id, viewerID)) return;
      editDraft.avatar_url = uploadedAvatarURL;
      pendingAvatarFile.value = null;
      revokePendingAvatarPreview();
      editAvatarLoadFailed.value = false;
    }

    if (pendingCoverFile.value) {
      phase = 'cover-upload';
      const uploadedCoverURL = await uploadProfileCover(pendingCoverFile.value);
      if (!isCurrentEditSession(capture, profile.id, viewerID)) return;
      editDraft.cover_image_url = uploadedCoverURL;
      pendingCoverFile.value = null;
      revokePendingCoverPreview();
      editCoverLoadFailed.value = false;
    }

    phase = 'profile-save';
    const payload = buildProfilePatch();
    if (Object.keys(payload).length === 0) {
      forceCloseEditProfile();
      return;
    }

    const updatedUser = await updateUserProfile(profile.id, payload);
    if (!isCurrentEditSession(capture, profile.id, viewerID)) return;
    profileStore.updateUser(updatedUser);
    authStore.syncCurrentIdentityProfile(updatedUser);
    forceCloseEditProfile();
  } catch (error) {
    if (isCurrentEditSession(capture, profile.id, viewerID)) {
      editError.value = profileEditErrorMessage(error, phase);
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

const retryInitialTimeline = () => {
  if (numericUserID.value !== null) {
    void profileStore.loadTimeline(numericUserID.value, true);
  }
};

const loadMoreTimeline = () => {
  if (profileViewActive.value && numericUserID.value !== null) {
    void profileStore.loadMoreTimeline(numericUserID.value);
  }
};

const retryLoadMore = () => {
  if (numericUserID.value !== null) {
    profileStore.retryLoadMoreTimeline(numericUserID.value);
  }
};

const handleDeletePost = async (postId: number) => {
  const removed = await profileStore.deletePost(postId, numericUserID.value ?? undefined);
  if (removed) {
    disconnectObserver();
    await nextTick(updateObserver);
  }
};

const handleLikeToggle = (postId: number) => {
  if (!authStore.isAuthenticated) {
    navigateToLogin();
    return;
  }
  void profileStore.toggleLike(postId, numericUserID.value ?? undefined);
};

const handleRepostToggle = (postId: number) => {
  if (!authStore.isAuthenticated) {
    navigateToLogin();
    return;
  }
  void profileStore.toggleRepost(postId, numericUserID.value ?? undefined);
};

const handleBookmarkToggle = (postId: number) => {
  if (!authStore.isAuthenticated) {
    navigateToLogin();
    return;
  }
  bookmarkMutationError.value = '';
  void profileStore.toggleBookmark(postId, numericUserID.value ?? undefined).then((result) => {
    if (!result) bookmarkMutationError.value = 'Could not update bookmark. Please try again.';
  });
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
  const activeSentinel = sentinelRef.value;
  const activeHasMore = hasMore.value;
  const activeLoadingMore = timelineLoadingMore.value;
  const activeLoadMoreError = timelineLoadMoreError.value;
  if (
    !profileViewActive.value
    || !intersectionObserverAvailable
    || !profileScrollViewportRef.value
    || !activeSentinel
    || !activeHasMore
    || activeLoadingMore
    || activeLoadMoreError
    || !canRenderProfile.value
    || !user.value
  ) {
    return;
  }

  const root = profileScrollViewportRef.value;
  observer = new IntersectionObserver((entries) => {
    if (
      profileViewActive.value
      && entries.some((entry) => entry.isIntersecting)
    ) {
      loadMoreTimeline();
    }
  }, { root, rootMargin: '240px 0px' });
  observer.observe(activeSentinel);
};

const deactivateProfileView = () => {
  if (!profileViewActive.value) {
    return;
  }

  profileViewActive.value = false;
  resumeOnActivation = true;

  forceCloseEditProfile();
  disconnectObserver();
};

const activateProfileView = () => {
  if (!resumeOnActivation) {
    return;
  }

  resumeOnActivation = false;
  profileViewActive.value = true;

  syncProfileRouteID();
  beginProfileRestoreEpoch();

  void nextTick(() => {
    if (!profileViewActive.value) {
      return;
    }

    void restoreScrollOnce();
    updateObserver();
  });
};

watch(
  [
    () => route.name,
    () => route.params.id,
  ],
  () => {
    syncProfileRouteID();
  },
);

watch(userId, (nextID, previousID) => {
  beginProfileRestoreEpoch();
  const previousNumericID = Number(previousID);
  if (Number.isSafeInteger(previousNumericID) && previousNumericID > 0) {
    saveCurrentScroll(previousNumericID);
    profileStore.cancelPendingDeletesForProfile(previousNumericID);
  }
  invalidProfileError.value = '';
  loadProfile();
}, { immediate: true });

watch(
  [currentViewerID, () => authStore.isAuthenticated],
  ([nextViewerID, nextAuthenticated], [previousViewerID, previousAuthenticated]) => {
    if (nextViewerID === previousViewerID && nextAuthenticated === previousAuthenticated) return;
    beginProfileRestoreEpoch();
    forceCloseEditProfile();
    loadProfile();
  },
);

watch(
  [
    userId,
    () => activeSession.value?.profileLoaded,
    () => activeSession.value?.timelineLoaded,
    () => activeSession.value?.timelineInitialLoading,
    () => activeSession.value?.timelineInitialError,
  ],
  () => { void restoreScrollOnce(); },
  { flush: 'post', immediate: true },
);

watch(
  [
    userId,
    () => activeSession.value?.profileLoaded,
    () => activeSession.value?.timelineLoaded,
    () => activeSession.value?.hasMore,
    () => activeSession.value?.timelineLoadingMore,
    () => activeSession.value?.timelineLoadMoreError,
    () => activeSession.value?.timelineItems.length,
  ],
  () => { void nextTick(updateObserver); },
  { flush: 'post' },
);

onMounted(() => {
  void nextTick(updateObserver);
});

onDeactivated(deactivateProfileView);
onActivated(activateProfileView);

onBeforeUnmount(() => {
  if (profileViewActive.value && numericUserID.value !== null) {
    saveCurrentScroll(numericUserID.value);
  }
  profileViewActive.value = false;
  resumeOnActivation = false;
  forceCloseEditProfile();
  disconnectObserver();
});
</script>

<style scoped>
.profile-view {
  display: flex;
  width: 100%;
  height: 100vh;
  height: 100dvh;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
  color: var(--color-text);
  background: var(--color-surface);
}

.profile-header {
  position: relative;
  z-index: 12;
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  min-height: 56px;
  padding: var(--space-2) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.profile-scroll-viewport {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior-y: contain;
  overflow-anchor: auto;
  -webkit-overflow-scrolling: touch;
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
  display: block;
  border-bottom: 1px solid var(--color-border);
}

.profile-cover,
.profile-edit-cover__preview {
  aspect-ratio: 3 / 1;
}

.profile-cover {
  position: relative;
  width: 100%;
  overflow: hidden;
  background: var(--color-surface-subtle);
}

.profile-cover__image {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
  object-position: center;
}

.profile-identity__body {
  padding: 0 var(--space-5) var(--space-5);
}

.profile-identity__top-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  min-height: 68px;
}

.profile-avatar--edit {
  position: relative;
  display: grid;
  place-items: center;
  border: 1px solid var(--color-border-strong);
  border-radius: 50%;
  background: var(--color-surface-subtle);
  color: var(--color-text-secondary);
  font-size: 28px;
  font-weight: 800;
}

.profile-identity .profile-avatar {
  margin-top: -56px;
  box-shadow: 0 0 0 4px var(--color-surface);
}

.profile-identity__copy {
  width: 100%;
  min-width: 0;
}

.profile-identity__action {
  display: grid;
  flex: 0 0 auto;
  justify-items: end;
  gap: var(--space-2);
  min-width: 0;
  margin-top: var(--space-3);
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
  display: inline-flex;
  align-items: baseline;
  gap: var(--space-1);
  color: inherit;
  text-decoration: none;
}

.profile-social__item {
  color: inherit;
  font-weight: 500;
}

.profile-social__item strong {
  color: var(--color-text);
  font-weight: 750;
}

.profile-social a:hover,
.profile-social a:focus-visible {
  color: var(--color-accent);
  text-decoration: underline;
  text-underline-offset: 3px;
}

.profile-social a:hover .profile-social__item,
.profile-social a:focus-visible .profile-social__item,
.profile-social a:hover .profile-social__item strong,
.profile-social a:focus-visible .profile-social__item strong {
  color: var(--color-accent);
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
  width: 112px;
  height: 112px;
  margin-top: -56px;
  border-radius: 50%;
  box-shadow: 0 0 0 4px var(--color-surface);
}

.profile-skeleton--cover {
  aspect-ratio: 3 / 1;
  border-radius: 0;
}

.profile-identity--loading .profile-identity__copy {
  display: grid;
  gap: var(--space-2);
  margin-top: var(--space-3);
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

@media (max-width: 799px) {
  .profile-view {
    height: calc(
      100vh
      - var(--mobile-safe-top)
      - var(--mobile-bottom-nav-height)
      - var(--mobile-safe-bottom)
    );
    height: calc(
      100dvh
      - var(--mobile-safe-top)
      - var(--mobile-bottom-nav-height)
      - var(--mobile-safe-bottom)
    );
  }
}

@media (max-width: 520px) {
  .profile-header {
    padding-inline: var(--space-3);
  }

  .profile-identity__body,
  .profile-skeleton-post,
  .profile-empty,
  .profile-state,
  .profile-feed-sentinel {
    padding-inline: var(--space-4);
  }

  .profile-identity__top-row {
    gap: var(--space-2);
  }
}

@media (max-width: 360px) {
  .profile-identity__top-row {
    gap: var(--space-1);
  }

  .profile-identity__action .profile-action,
  .profile-identity__action .profile-follow-button {
    padding-inline: var(--space-3);
  }
}

.profile-avatar--edit img {
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

.profile-action--primary:focus-visible {
  border-color: var(--color-accent);
  background: var(--color-accent);
  color: #fff;
}

.profile-edit-dialog {
  width: min(calc(100% - 32px), 540px);
  max-width: calc(100% - 32px);
  max-height: min(90vh, 820px);
  max-height: min(90dvh, 820px);
  box-sizing: border-box;
  margin: auto;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  padding: 0;
  overflow: hidden;
  background: var(--color-surface);
  color: var(--color-text);
}

.profile-edit-dialog::backdrop {
  background: rgba(15, 20, 25, 0.62);
}

.profile-edit-form {
  display: grid;
  gap: var(--space-4);
  max-height: min(90vh, 820px);
  max-height: min(90dvh, 820px);
  box-sizing: border-box;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: 0 var(--space-6);
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

.profile-edit-dialog__header {
  position: sticky;
  top: 0;
  z-index: 3;
  min-height: 64px;
  box-sizing: border-box;
  margin-inline: calc(-1 * var(--space-6));
  padding-inline: var(--space-6);
  border-bottom: 1px solid var(--color-border);
  background: var(--color-surface);
}

.profile-edit-dialog__header h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 750;
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

.profile-edit-dialog__close:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-text);
}

@media (hover: hover) and (pointer: fine) {
  .profile-header__back:hover {
    background: var(--color-surface-subtle);
  }

  .profile-follow-button:hover {
    border-color: var(--color-text);
  }

  .profile-action:hover {
    border-color: var(--color-accent);
    color: var(--color-accent);
  }

  .profile-action--primary:hover {
    border-color: var(--color-accent);
    background: var(--color-accent);
    color: #fff;
  }

  .profile-edit-dialog__close:hover {
    background: var(--color-surface-subtle);
    color: var(--color-text);
  }
}

.profile-edit-dialog__close:disabled {
  cursor: wait;
  opacity: 0.55;
}

.profile-edit-avatar {
  display: flex;
  align-items: flex-start;
  gap: var(--space-4);
}

.profile-edit-cover {
  display: grid;
  gap: var(--space-2);
}

.profile-edit-cover__preview {
  position: relative;
  width: 100%;
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-sm);
  background: var(--color-surface-subtle);
}

.profile-edit-cover__preview img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
  object-position: center;
}

.profile-edit-media-change {
  display: inline-grid;
  width: 44px;
  height: 44px;
  place-items: center;
  border: 0;
  border-radius: 50%;
  background: rgba(15, 20, 25, 0.72);
  color: #fff;
  cursor: pointer;
  padding: 0;
  font: inherit;
}

.profile-edit-cover__change,
.profile-edit-avatar__change {
  position: absolute;
  top: 50%;
  left: 50%;
  z-index: 1;
  transform: translate(-50%, -50%);
}

.profile-edit-media-change:disabled {
  cursor: wait;
  opacity: 0.55;
}

.profile-edit-media-change:focus-visible {
  outline: 2px solid #fff;
  outline-offset: 2px;
}

@media (hover: hover) and (pointer: fine) {
  .profile-edit-media-change:hover:not(:disabled) {
    background: rgba(15, 20, 25, 0.86);
  }
}

.profile-edit-cover__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.profile-edit-media-remove {
  min-height: 36px;
  border: 0;
  padding: 0 var(--space-2);
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
  font-weight: 650;
}

.profile-edit-media-remove:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.profile-edit-media-remove:focus-visible {
  color: var(--color-danger);
  outline: 2px solid currentColor;
  outline-offset: 2px;
}

@media (hover: hover) and (pointer: fine) {
  .profile-edit-media-remove:hover:not(:disabled) {
    color: var(--color-danger);
  }
}

@media (max-width: 520px) {
  .profile-edit-media-remove {
    min-height: 44px;
  }
}

.profile-avatar--edit {
  width: 80px;
  height: 80px;
  flex: 0 0 auto;
  overflow: hidden;
  border-radius: 50%;
  font-size: 30px;
}

.profile-edit-avatar__copy {
  display: grid;
  flex: 1 1 auto;
  gap: var(--space-2);
  min-width: 0;
}

.profile-edit-avatar__actions {
  flex-wrap: wrap;
  gap: var(--space-2);
}

.profile-edit-field {
  display: grid;
  gap: var(--space-2);
}

.profile-edit-field__label,
.profile-edit-field__label-row {
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

.profile-edit-error {
  margin: 0;
  color: var(--color-danger);
  font-size: 13px;
  line-height: 1.4;
}

.profile-edit-dialog__actions {
  position: sticky;
  bottom: 0;
  z-index: 2;
  justify-content: flex-end;
  gap: var(--space-2);
  margin-inline: calc(-1 * var(--space-6));
  padding: var(--space-4) var(--space-6) var(--space-5);
  border-top: 1px solid var(--color-border);
  background: var(--color-surface);
}

.profile-edit-dialog__actions > button {
  min-width: 92px;
  min-height: 40px;
}

.profile-edit-dialog .profile-follow-button:disabled {
  border-color: var(--color-border);
  background: var(--color-surface-subtle);
  color: var(--color-text-tertiary);
  cursor: not-allowed;
  opacity: 0.52;
}

.profile-edit-dialog__close:disabled,
.profile-edit-dialog .profile-action:disabled {
  cursor: wait;
  opacity: 0.62;
}

@media (max-width: 520px) {
  .profile-edit-dialog {
    width: 100%;
    max-width: none;
    height: 100vh;
    max-height: 100vh;
    height: 100dvh;
    max-height: 100dvh;
    margin: 0;
    border: 0;
    border-radius: 0;
  }

  .profile-edit-form {
    max-height: 100vh;
    max-height: 100dvh;
    padding-inline: var(--space-4);
  }

  .profile-edit-dialog__header {
    min-height: 60px;
    margin-inline: calc(-1 * var(--space-4));
    padding-inline: var(--space-4);
  }

  .profile-edit-dialog__actions {
    margin-inline: calc(-1 * var(--space-4));
    padding: var(--space-3) var(--space-4) calc(var(--space-4) + env(safe-area-inset-bottom));
  }

  .profile-edit-dialog__actions > button {
    min-height: 42px;
  }

  .profile-edit-avatar {
    align-items: flex-start;
  }
}

@media (max-width: 360px) {
  .profile-edit-form {
    padding-inline: var(--space-3);
  }

  .profile-edit-dialog__header {
    margin-inline: calc(-1 * var(--space-3));
    padding-inline: var(--space-3);
  }

  .profile-edit-dialog__actions {
    margin-inline: calc(-1 * var(--space-3));
    padding-inline: var(--space-3);
  }

  .profile-edit-avatar {
    gap: var(--space-3);
  }
}
</style>
