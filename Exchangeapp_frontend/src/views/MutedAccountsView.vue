<template>
  <main class="muted-accounts-view">
    <header class="muted-accounts-view__header">
      <button class="muted-accounts-view__back" type="button" aria-label="Back" @click="goBack">
        <AppIcon name="arrow-left" :size="20" />
      </button>
      <div class="muted-accounts-view__heading">
        <p class="muted-accounts-view__eyebrow">YOUR ACCOUNT</p>
        <h1>Muted accounts</h1>
      </div>
      <MobileAccountMenu v-if="currentViewerID !== null" />
    </header>

    <section v-if="currentViewerID === null" class="muted-accounts-view__state">
      <h2>Log in to manage muted accounts.</h2>
      <RouterLink
        class="muted-accounts-view__action"
        :to="{ name: 'Login', query: { returnTo: route.fullPath || '/settings/muted' } }"
      >
        Log in
      </RouterLink>
    </section>
    <section v-else-if="listLoading && items.length === 0" class="muted-accounts-view__state" aria-live="polite">
      <p>Loading muted accounts…</p>
    </section>
    <section v-else-if="listError && items.length === 0" class="muted-accounts-view__state" role="alert">
      <p>Muted accounts could not be loaded.</p>
      <button class="muted-accounts-view__action" type="button" @click="userMuteStore.loadMutedAccounts(true)">
        Try again
      </button>
    </section>
    <section v-else-if="items.length === 0" class="muted-accounts-view__state">
      <h2>You haven’t muted anyone.</h2>
      <p>Accounts you mute will appear here.</p>
    </section>
    <section v-else class="muted-accounts-view__list" aria-label="Muted accounts">
      <article v-for="item in items" :key="item.user.id" class="muted-account">
        <RouterLink
          class="muted-account__identity"
          :to="{ name: 'UserProfile', params: { id: String(item.user.id) } }"
        >
          <UserAvatar
            :avatar-url="item.user.avatar_url"
            :display-name="item.user.display_name"
            :username="item.user.username"
            :size="44"
          />
          <span class="muted-account__copy">
            <strong>{{ item.user.display_name || item.user.username }}</strong>
            <span>@{{ item.user.username }}</span>
          </span>
        </RouterLink>
        <button
          class="muted-account__unmute"
          type="button"
          :disabled="userMuteStore.isPending(item.user.id)"
          :aria-busy="userMuteStore.isPending(item.user.id)"
          @click="handleUnmute(item.user.id, item.user.username)"
        >
          {{ userMuteStore.isPending(item.user.id) ? 'Updating…' : 'Unmute' }}
        </button>
      </article>

      <p v-if="listError" class="muted-accounts-view__inline-error" role="alert">
        Muted accounts could not be refreshed.
        <button type="button" @click="userMuteStore.loadMutedAccounts(true)">Try again</button>
      </p>
      <p v-if="listLoading" class="muted-accounts-view__load-state" aria-live="polite">Refreshing…</p>
      <p v-if="listLoadingMoreError" class="muted-accounts-view__inline-error" role="alert">
        Couldn’t load more accounts.
        <button type="button" @click="userMuteStore.loadMoreMutedAccounts">Try again</button>
      </p>
      <button
        v-if="nextCursor"
        class="muted-accounts-view__load-more"
        type="button"
        :disabled="listLoadingMore || listStale"
        :aria-busy="listLoadingMore"
        @click="userMuteStore.loadMoreMutedAccounts"
      >
        {{ listLoadingMore ? 'Loading…' : listStale ? 'Refreshing list…' : 'Load more' }}
      </button>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { useRoute, useRouter } from 'vue-router';
import AppIcon from '../components/icons/AppIcon.vue';
import MobileAccountMenu from '../components/layout/MobileAccountMenu.vue';
import UserAvatar from '../components/users/UserAvatar.vue';
import { useAuthStore } from '../store/auth';
import { useUserMuteStore } from '../store/userMute';
import { showUserMuteError, showUserUnmuteSuccess } from '../utils/userMuteFeedback';

const authStore = useAuthStore();
const userMuteStore = useUserMuteStore();
const route = useRoute();
const router = useRouter();
const {
  items,
  nextCursor,
  listLoading,
  listLoadingMore,
  listError,
  listLoadingMoreError,
  listStale,
} = storeToRefs(userMuteStore);

const currentViewerID = computed(() => {
  const id = Number(authStore.currentIdentity?.id);
  return authStore.isAuthenticated && Number.isSafeInteger(id) && id > 0 ? id : null;
});

const goBack = () => {
  const historyState = window.history.state as { back?: string | null } | null;
  if (historyState?.back) {
    router.back();
  } else {
    void router.push({ name: 'Home' });
  }
};

const handleUnmute = async (userID: number, username: string) => {
  const result = await userMuteStore.unmuteAuthor(userID);
  if (result === 'failed') {
    showUserMuteError();
    return;
  }
  if (result === 'updated') {
    showUserUnmuteSuccess(username);
    await userMuteStore.loadMutedAccounts(true);
  }
};

watch(currentViewerID, (viewerID) => {
  userMuteStore.setViewer(viewerID);
  if (viewerID !== null) void userMuteStore.loadMutedAccounts(true);
}, { immediate: true });
</script>

<style scoped>
.muted-accounts-view {
  display: flex;
  width: min(100%, 760px);
  min-height: 100%;
  flex-direction: column;
  margin: 0 auto;
  padding: 24px clamp(16px, 4vw, 32px) 64px;
  box-sizing: border-box;
  color: var(--color-text);
}

.muted-accounts-view__header {
  display: flex;
  min-height: 72px;
  align-items: center;
  gap: 14px;
  border-bottom: 1px solid var(--color-border);
  padding-bottom: 18px;
}

.muted-accounts-view__back {
  display: grid;
  width: 44px;
  height: 44px;
  flex: 0 0 auto;
  place-items: center;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
}

.muted-accounts-view__back:hover,
.muted-accounts-view__back:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-text);
}

.muted-accounts-view__back:focus-visible,
.muted-accounts-view__action:focus-visible,
.muted-account__identity:focus-visible,
.muted-account__unmute:focus-visible,
.muted-accounts-view__load-more:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

.muted-accounts-view__heading {
  flex: 1 1 auto;
  min-width: 0;
}

.muted-accounts-view__eyebrow {
  margin: 0 0 4px;
  color: var(--color-accent);
  font-size: 11px;
  font-weight: 800;
  letter-spacing: 0.14em;
}

.muted-accounts-view h1 {
  margin: 0;
  font-size: clamp(24px, 4vw, 32px);
  letter-spacing: -0.035em;
}

.muted-accounts-view__state {
  display: grid;
  min-height: 260px;
  place-items: center;
  align-content: center;
  gap: 10px;
  color: var(--color-text-secondary);
  text-align: center;
}

.muted-accounts-view__state h2,
.muted-accounts-view__state p {
  margin: 0;
}

.muted-accounts-view__state h2 {
  color: var(--color-text);
  font-size: 18px;
}

.muted-accounts-view__action,
.muted-accounts-view__load-more,
.muted-account__unmute {
  display: inline-flex;
  min-height: 40px;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-border-strong, var(--color-border));
  border-radius: var(--radius-pill);
  padding: 0 16px;
  background: var(--color-surface);
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
  font-weight: 700;
  text-decoration: none;
}

.muted-accounts-view__action:hover,
.muted-accounts-view__load-more:hover,
.muted-account__unmute:hover {
  border-color: var(--color-accent);
  color: var(--color-accent);
}

.muted-accounts-view__list {
  display: grid;
  gap: 2px;
  padding-top: 8px;
}

.muted-account {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  border-bottom: 1px solid var(--color-border);
  padding: 14px 2px;
}

.muted-account__identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 12px;
  color: inherit;
  text-decoration: none;
}

.muted-account__copy {
  display: grid;
  min-width: 0;
  gap: 3px;
}

.muted-account__copy strong,
.muted-account__copy span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.muted-account__copy strong {
  color: var(--color-text);
  font-size: 14px;
}

.muted-account__copy span {
  color: var(--color-text-tertiary);
  font-size: 13px;
}

.muted-account__unmute {
  flex: 0 0 auto;
}

.muted-account__unmute:disabled,
.muted-accounts-view__load-more:disabled {
  cursor: wait;
  opacity: 0.65;
}

.muted-accounts-view__load-more {
  justify-self: center;
  margin-top: 18px;
}

.muted-accounts-view__inline-error,
.muted-accounts-view__load-state {
  margin: 12px 0 0;
  color: var(--color-text-secondary);
  font-size: 13px;
  text-align: center;
}

.muted-accounts-view__inline-error button {
  border: 0;
  background: transparent;
  color: var(--color-accent);
  cursor: pointer;
  font: inherit;
  font-weight: 700;
}

@media (max-width: 480px) {
  .muted-accounts-view {
    padding-top: 16px;
  }

  .muted-account {
    gap: 8px;
  }

  .muted-account__unmute {
    padding: 0 13px;
  }
}
</style>
