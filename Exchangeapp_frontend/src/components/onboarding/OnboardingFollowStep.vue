<template>
  <section class="onboarding-follow" aria-labelledby="onboarding-title" :aria-busy="loadingUsers">
    <div v-if="loadingUsers" class="onboarding-follow__status" role="status">Finding a few people for you…</div>
    <div v-else-if="loadFailed" class="onboarding-follow__status">
      <p>Suggestions are unavailable right now.</p>
      <button class="onboarding-follow__retry" type="button" @click="loadSuggestions">Try again</button>
    </div>
    <div v-else-if="users.length === 0" class="onboarding-follow__empty">
      <span class="onboarding-follow__empty-mark" aria-hidden="true">↗</span>
      <strong>No suggestions available right now.</strong>
      <p>You can discover more people later.</p>
    </div>
    <div v-else class="onboarding-follow__list" aria-label="Suggested people to follow">
      <article v-for="item in users" :key="item.user.id" class="onboarding-follow__row">
        <UserAvatar
          :avatar-url="item.user.avatar_url"
          :display-name="item.user.display_name"
          :username="item.user.username"
          :size="52"
          decorative
        />
        <div class="onboarding-follow__identity">
          <strong>{{ item.user.display_name.trim() || item.user.username }}</strong>
          <span>@{{ item.user.username }}</span>
          <p v-if="item.user.bio">{{ item.user.bio }}</p>
        </div>
        <div class="onboarding-follow__action">
          <button
            type="button"
            class="onboarding-follow__button"
            :class="{ 'onboarding-follow__button--following': item.following }"
            :aria-pressed="item.following"
            :aria-busy="pendingIDs.has(item.user.id)"
            :disabled="onboarding.saving"
            @click="toggleFollow(item.user.id)"
          >
            {{ item.following ? 'Following' : 'Follow' }}
          </button>
          <span v-if="errors.get(item.user.id)" class="onboarding-follow__row-error" role="alert">
            {{ errors.get(item.user.id) }}
          </span>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue';
import { followUser, getSuggestedUsers, unfollowUser, type UserConnectionItem } from '../../services/userService';
import UserAvatar from '../users/UserAvatar.vue';
import { registerSuggestedUsersSessionSync, syncExternalFollowState } from '../../store/sessionSync';
import { useOnboardingStore } from '../../store/onboarding';

const onboarding = useOnboardingStore();
const users = ref<UserConnectionItem[]>([]);
const loadingUsers = ref(true);
const loadFailed = ref(false);
const pendingIDs = reactive(new Set<number>());
const errors = reactive(new Map<number, string>());
let active = true;
let requestVersion = 0;

const applyExternalFollowStateLocal = (state: { user_id: number; following: boolean }) => {
  const item = users.value.find(candidate => candidate.user.id === state.user_id);
  if (!item) return false;
  item.following = state.following;
  return true;
};
const unregisterSync = registerSuggestedUsersSessionSync({ applyExternalFollowStateLocal });

const loadSuggestions = async () => {
  const context = onboarding.captureContext();
  if (!context || loadingUsers.value && requestVersion > 0) return;
  const version = ++requestVersion;
  loadingUsers.value = true;
  loadFailed.value = false;
  errors.clear();
  onboarding.clearError();
  try {
    const response = await getSuggestedUsers(5);
    if (!active || version !== requestVersion || !onboarding.matchesContext(context)) return;
    if (!Array.isArray(response.items)) throw new Error('Invalid suggestions response');
    const seen = new Set<number>([context.binding.userID]);
    users.value = response.items.filter(item => {
      const id = item?.user?.id;
      if (!Number.isSafeInteger(id) || id <= 0 || seen.has(id)) return false;
      seen.add(id);
      return true;
    }).slice(0, 5);
  } catch {
    if (!active || version !== requestVersion || !onboarding.matchesContext(context)) return;
    loadFailed.value = true;
    users.value = [];
    onboarding.setError('Suggested people could not be loaded. You can retry or finish setup.', true);
  } finally {
    if (active && version === requestVersion && onboarding.matchesContext(context)) loadingUsers.value = false;
  }
};

const toggleFollow = async (userID: number) => {
  const item = users.value.find(candidate => candidate.user.id === userID);
  if (!item || pendingIDs.has(userID)) return;
  const context = onboarding.beginOperation();
  if (!context) return;
  pendingIDs.add(userID);
  errors.delete(userID);
  try {
    const state = item.following ? await unfollowUser(userID) : await followUser(userID);
    if (!onboarding.matchesContext(context)) return;
    if (state.user_id !== userID || typeof state.following !== 'boolean') {
      throw new Error('Invalid follow response');
    }
    item.following = state.following;
    syncExternalFollowState(state);
  } catch {
    if (onboarding.matchesContext(context)) {
      errors.set(userID, 'Could not update follow status. Retry.');
      onboarding.setError('A follow change could not be saved. Retry the row or continue setup.', true);
    }
  } finally {
    if (onboarding.matchesContext(context)) pendingIDs.delete(userID);
    onboarding.finishOperation(context);
  }
};

const next = async () => { await onboarding.complete(); };
const skip = async () => { await onboarding.complete(); };
defineExpose({ next, skip, canAdvance: computed(() => true) });

onMounted(() => { void loadSuggestions(); });
onBeforeUnmount(() => {
  active = false;
  requestVersion += 1;
  pendingIDs.clear();
  errors.clear();
  users.value = [];
  unregisterSync();
});
</script>

<style scoped>
.onboarding-follow { min-height: 100%; }
.onboarding-follow__list { display: grid; }
.onboarding-follow__row { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: var(--space-3); min-width: 0; padding: var(--space-4) var(--space-5); border-bottom: 1px solid var(--color-border); }
.onboarding-follow__identity { display: grid; min-width: 0; gap: 2px; }
.onboarding-follow__identity strong, .onboarding-follow__identity span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.onboarding-follow__identity strong { color: var(--color-text); font-size: 14px; }
.onboarding-follow__identity span { color: var(--color-text-secondary); font-size: 13px; }
.onboarding-follow__identity p { display: -webkit-box; margin: var(--space-1) 0 0; overflow: hidden; color: var(--color-text-secondary); font-size: 12px; line-height: 1.4; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.onboarding-follow__action { display: grid; justify-items: end; gap: var(--space-1); }
.onboarding-follow__button { min-width: 92px; min-height: 44px; border: 1px solid var(--color-text); border-radius: var(--radius-pill); background: var(--color-text); color: var(--color-surface); font: inherit; font-size: 13px; font-weight: 750; cursor: pointer; }
.onboarding-follow__button--following { border-color: var(--color-border-strong); background: var(--color-surface); color: var(--color-text); }
.onboarding-follow__button:hover:not(:disabled) { opacity: .84; }
.onboarding-follow__button:disabled { cursor: wait; opacity: .58; }
.onboarding-follow__row-error { max-width: 120px; color: var(--color-danger); font-size: 11px; text-align: right; }
.onboarding-follow__empty, .onboarding-follow__status { display: grid; min-height: 200px; align-content: center; justify-items: center; gap: var(--space-2); padding: var(--space-6); color: var(--color-text-secondary); text-align: center; }
.onboarding-follow__empty strong { color: var(--color-text); }
.onboarding-follow__empty p, .onboarding-follow__status p { margin: 0; font-size: 14px; }
.onboarding-follow__empty-mark { display: grid; width: 46px; height: 46px; place-items: center; margin-bottom: var(--space-2); border-radius: 50%; background: var(--color-surface-subtle); color: var(--color-accent); font-size: 24px; }
.onboarding-follow__retry { min-height: 44px; margin-top: var(--space-2); border: 1px solid var(--color-border-strong); border-radius: var(--radius-pill); padding-inline: var(--space-5); background: var(--color-surface); color: var(--color-text); font: inherit; font-weight: 700; cursor: pointer; }
button:focus-visible { outline: 3px solid color-mix(in srgb, var(--color-accent) 45%, transparent); outline-offset: 3px; }
@media (max-width: 380px) { .onboarding-follow__row { grid-template-columns: auto minmax(0, 1fr); padding-inline: var(--space-3); } .onboarding-follow__action { grid-column: 2; grid-row: 2; justify-self: start; justify-items: start; } }
</style>
