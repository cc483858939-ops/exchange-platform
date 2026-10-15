<template>
  <section class="onboarding-avatar" aria-labelledby="onboarding-title">
    <div class="onboarding-avatar__preview">
      <UserAvatar
        :avatar-url="previewURL || uploadedAvatarURL || identity?.avatar_url"
        :display-name="identity?.display_name"
        :username="identity?.username"
        :size="152"
        decorative
      />
      <button
        class="onboarding-avatar__camera"
        type="button"
        aria-label="Choose a profile photo"
        :disabled="onboarding.saving"
        @click="openPicker"
      >
        <span aria-hidden="true">＋</span>
      </button>
    </div>
    <input
      ref="fileInput"
      class="onboarding-avatar__input"
      type="file"
      accept="image/jpeg,image/png,image/webp"
      aria-label="Choose a JPEG, PNG, or WebP profile photo"
      tabindex="-1"
      @change="handleSelection"
    />
    <button class="onboarding-avatar__choose" type="button" :disabled="onboarding.saving" @click="openPicker">
      {{ previewURL || uploadedAvatarURL ? 'Change photo' : 'Choose a photo' }}
    </button>
    <p class="onboarding-avatar__hint">JPEG, PNG or WebP · Up to 10 MB</p>

    <AvatarCropDialog
      v-if="cropSourceFile"
      :file="cropSourceFile"
      @cancel="handleCropCancel"
      @apply="handleCropApply"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue';
import AvatarCropDialog from '../profile/AvatarCropDialog.vue';
import UserAvatar from '../users/UserAvatar.vue';
import { uploadProfileAvatar, updateUserProfile } from '../../services/userService';
import { useAuthStore } from '../../store/auth';
import { useOnboardingStore } from '../../store/onboarding';
import { useProfileSessionStore } from '../../store/profileSession';
import type { OnboardingRequestContext } from '../../store/onboarding';

const sourceMaxBytes = 10 * 1024 * 1024;
const croppedMaxBytes = 2 * 1024 * 1024;
const acceptedTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);

const authStore = useAuthStore();
const profileStore = useProfileSessionStore();
const onboarding = useOnboardingStore();
const fileInput = ref<HTMLInputElement | null>(null);
const cropSourceFile = ref<File | null>(null);
const croppedFile = ref<File | null>(null);
const previewURL = ref('');
const uploadedAvatarURL = ref('');
const profileSaved = ref(false);
const identity = computed(() => authStore.currentIdentity);

const revokePreview = () => {
  if (!previewURL.value) return;
  URL.revokeObjectURL(previewURL.value);
  previewURL.value = '';
};

const openPicker = () => {
  if (!onboarding.saving) fileInput.value?.click();
};

const handleSelection = (event: Event) => {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file) return;
  if (file.size <= 0 || file.size > sourceMaxBytes) {
    onboarding.setError('Photo must be between 1 byte and 10 MB.');
    return;
  }
  if (!acceptedTypes.has(file.type)) {
    onboarding.setError('Use a JPEG, PNG, or WebP image.');
    return;
  }
  onboarding.clearError();
  cropSourceFile.value = file;
};

const handleCropCancel = () => {
  cropSourceFile.value = null;
  void nextTick(() => document.querySelector<HTMLButtonElement>('.onboarding-avatar__camera')?.focus());
};

const handleCropApply = (file: File) => {
  cropSourceFile.value = null;
  if (file.size <= 0 || file.size > croppedMaxBytes) {
    onboarding.setError('Could not prepare this photo. Try another image.');
    return;
  }
  revokePreview();
  croppedFile.value = file;
  uploadedAvatarURL.value = '';
  profileSaved.value = false;
  try {
    previewURL.value = URL.createObjectURL(file);
    onboarding.clearError();
  } catch {
    onboarding.setError('Could not preview this photo. Choose it again.');
    croppedFile.value = null;
  }
  void nextTick(() => document.querySelector<HTMLButtonElement>('.onboarding-avatar__camera')?.focus());
};

const saveProgress = async (context: OnboardingRequestContext) => {
  const saved = await onboarding.saveProgress('display_name', context);
  if (!onboarding.matchesContext(context) || !saved) return;
  croppedFile.value = null;
  cropSourceFile.value = null;
  uploadedAvatarURL.value = '';
  profileSaved.value = false;
  revokePreview();
};

const next = async () => {
  const context = onboarding.beginOperation();
  if (!context) return;
  try {
    if (croppedFile.value && !uploadedAvatarURL.value) {
      uploadedAvatarURL.value = await uploadProfileAvatar(croppedFile.value);
      if (!onboarding.matchesContext(context)) return;
    }
    if (uploadedAvatarURL.value && !profileSaved.value) {
      const updated = await updateUserProfile(context.binding.userID, {
        avatar_url: uploadedAvatarURL.value,
      });
      if (!onboarding.matchesContext(context)) return;
      if (updated.id !== context.binding.userID) throw new Error('Profile response did not match the signed-in user.');
      if (!authStore.syncCurrentIdentityProfile(updated)) throw new Error('Could not sync the saved profile.');
      profileStore.updateUser(updated);
      profileSaved.value = true;
      croppedFile.value = null;
      revokePreview();
    }
    await saveProgress(context);
  } catch {
    if (onboarding.matchesContext(context)) {
      onboarding.setError('Could not save your photo. Please retry.', true);
    }
  } finally {
    onboarding.finishOperation(context);
  }
};

const skip = async () => {
  cropSourceFile.value = null;
  croppedFile.value = null;
  uploadedAvatarURL.value = '';
  profileSaved.value = false;
  revokePreview();
  const context = onboarding.beginOperation();
  if (!context) return;
  try {
    await saveProgress(context);
  } finally {
    onboarding.finishOperation(context);
  }
};

const canAdvance = computed(() => !cropSourceFile.value);

defineExpose({ next, skip, canAdvance });

onBeforeUnmount(() => {
  cropSourceFile.value = null;
  croppedFile.value = null;
  uploadedAvatarURL.value = '';
  revokePreview();
});
</script>

<style scoped>
.onboarding-avatar { display: grid; justify-items: center; align-content: center; gap: var(--space-4); min-height: 100%; padding: var(--space-5) var(--space-4); text-align: center; }
.onboarding-avatar__preview { position: relative; display: grid; place-items: center; width: 168px; height: 168px; }
.onboarding-avatar__camera { position: absolute; right: 2px; bottom: 2px; display: grid; width: 48px; height: 48px; place-items: center; border: 4px solid var(--color-surface); border-radius: 50%; background: var(--color-text); color: var(--color-surface); font-size: 28px; line-height: 1; cursor: pointer; }
.onboarding-avatar__camera:hover { background: var(--color-accent); }
.onboarding-avatar__camera:disabled { opacity: .55; cursor: wait; }
.onboarding-avatar__input { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; clip-path: inset(50%); }
.onboarding-avatar__choose { min-height: 44px; padding: 0 var(--space-6); border: 1px solid var(--color-border-strong); border-radius: var(--radius-pill); background: var(--color-surface); color: var(--color-text); font: inherit; font-weight: 750; cursor: pointer; }
.onboarding-avatar__choose:hover { background: var(--color-surface-subtle); }
.onboarding-avatar__hint { margin: 0; color: var(--color-text-tertiary); font-size: 13px; }
button:focus-visible { outline: 3px solid color-mix(in srgb, var(--color-accent) 45%, transparent); outline-offset: 3px; }
</style>
