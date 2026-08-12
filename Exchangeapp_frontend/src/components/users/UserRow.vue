<template>
  <article class="user-row">
    <RouterLink class="user-row__identity" :to="{ name: 'UserProfile', params: { id: item.user.id } }">
      <span class="user-row__avatar" aria-hidden="true">
        <img v-if="avatarURL && !avatarLoadFailed" :src="avatarURL" alt="" @error="avatarLoadFailed = true" />
        <span v-else>{{ initial }}</span>
      </span>
      <span class="user-row__copy">
        <strong>{{ displayName }}</strong>
        <span>@{{ username }}</span>
        <small v-if="item.user.bio">{{ item.user.bio }}</small>
      </span>
    </RouterLink>
    <div class="user-row__action">
      <button v-if="!isSelf" class="user-row__follow" :class="{ 'user-row__follow--following': item.following }" type="button" :aria-pressed="item.following" :aria-busy="pending" :disabled="pending" @click="$emit('toggle-follow', item.user.id)">
        {{ item.following ? 'Following' : 'Follow' }}
      </button>
      <p v-if="error" class="user-row__error" aria-live="polite">{{ error }}</p>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { UserConnectionItem } from '../../services/userService';

const props = defineProps<{ item: UserConnectionItem; pending: boolean; error?: string; isSelf: boolean }>();
defineEmits<{ 'toggle-follow': [userID: number] }>();
const avatarLoadFailed = ref(false);
const username = computed(() => props.item.user.username.trim() || '?');
const displayName = computed(() => props.item.user.display_name.trim() || username.value);
const initial = computed(() => Array.from(displayName.value)[0]?.toUpperCase() || '?');
const avatarURL = computed(() => props.item.user.avatar_url.trim());
watch(() => [props.item.user.id, props.item.user.avatar_url], () => { avatarLoadFailed.value = false; });
</script>

<style scoped>
.user-row { display: flex; align-items: flex-start; gap: var(--space-4); min-width: 0; padding: var(--space-4) var(--space-5); border-bottom: 1px solid var(--color-border); }
.user-row__identity { display: flex; min-width: 0; flex: 1 1 auto; gap: var(--space-3); color: inherit; text-decoration: none; }
.user-row__identity:hover strong, .user-row__identity:focus-visible strong { color: var(--color-accent); text-decoration: underline; text-underline-offset: 3px; }
.user-row__avatar { display: grid; width: 46px; height: 46px; flex: 0 0 auto; place-items: center; overflow: hidden; border: 1px solid var(--color-border-strong); border-radius: 50%; background: var(--color-surface-subtle); color: var(--color-text-secondary); font-size: 17px; font-weight: 800; }
.user-row__avatar img { width: 100%; height: 100%; object-fit: cover; }
.user-row__copy { display: grid; min-width: 0; gap: 2px; padding-top: 2px; }
.user-row__copy strong, .user-row__copy span, .user-row__copy small { overflow: hidden; text-overflow: ellipsis; }
.user-row__copy strong { color: var(--color-text); font-size: 15px; line-height: 1.25; }
.user-row__copy span { color: var(--color-text-tertiary); font-size: 13px; }
.user-row__copy small { margin-top: var(--space-1); color: var(--color-text-secondary); font-size: 13px; line-height: 1.4; overflow-wrap: anywhere; }
.user-row__action { display: grid; flex: 0 0 auto; justify-items: end; gap: var(--space-2); }
.user-row__follow { min-width: 86px; min-height: 36px; border: 1px solid var(--color-accent); border-radius: var(--radius-pill); background: var(--color-accent); color: #fff; font: inherit; font-size: 13px; font-weight: 750; cursor: pointer; }
.user-row__follow--following { border-color: var(--color-border-strong); background: var(--color-surface); color: var(--color-text); }
.user-row__follow:disabled { cursor: wait; opacity: .62; }
.user-row__error { max-width: 120px; margin: 0; color: var(--color-danger); font-size: 12px; line-height: 1.35; text-align: right; }
@media (max-width: 380px) { .user-row { gap: var(--space-3); padding-inline: var(--space-4); } .user-row__avatar { width: 42px; height: 42px; } .user-row__follow { min-width: 72px; padding-inline: var(--space-2); } }
</style>
