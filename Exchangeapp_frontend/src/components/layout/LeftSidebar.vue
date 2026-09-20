<template>
  <div class="left-sidebar">
    <router-link
      class="left-sidebar__brand"
      :to="{ name: 'Home' }"
      aria-label="Exchange home"
      title="Exchange"
    >
      <span class="left-sidebar__brand-mark" aria-hidden="true"><BrandMark /></span>
    </router-link>

    <nav class="left-sidebar__nav" aria-label="Main navigation">
      <router-link
        v-for="item in visibleNavigation"
        :key="item.name"
        class="left-sidebar__link left-sidebar__link--icon"
        :class="{
          'left-sidebar__link--compact-only': item.compactOnly,
          'left-sidebar__link--primary': item.primary,
        }"
        :to="navigationDestination(item)"
        :aria-label="item.label"
        :title="item.label"
        @click.capture="handleNavigationClick($event, item)"
      >
        <AppIcon :name="item.icon" :size="item.iconSize" />
        <span class="left-sidebar__label">{{ item.label }}</span>
        <span
          v-if="item.name === 'Notifications' && notificationBadge"
          class="left-sidebar__badge"
          aria-label="Unread notifications"
        >{{ notificationBadge }}</span>
      </router-link>
    </nav>

    <div class="left-sidebar__account">
      <template v-if="authStore.isAuthenticated">
        <button
          class="left-sidebar__link left-sidebar__link--icon left-sidebar__logout"
          type="button"
          aria-label="Log out"
          title="Log out"
          @click="handleLogout"
        >
          <AppIcon name="logout" :size="26" />
          <span class="left-sidebar__label">Log out</span>
        </button>
      </template>
      <template v-else>
        <router-link class="left-sidebar__link" :to="{ name: 'Login' }">
          <span class="left-sidebar__label">Log in</span>
        </router-link>
        <router-link class="left-sidebar__link left-sidebar__signup" :to="{ name: 'Register' }">
          <span class="left-sidebar__label">Sign up</span>
        </router-link>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { useLogout } from '../../composables/useLogout';
import { useHomeTimelineStore } from '../../store/homeTimeline';
import { useNotificationStore } from '../../store/notification';
import { useSearchSessionStore } from '../../store/searchSession';
import BrandMark from '../brand/BrandMark.vue';
import AppIcon from '../icons/AppIcon.vue';

const props = withDefaults(defineProps<{ notificationBadge?: string | null }>(), {
  notificationBadge: null,
});

const { authStore, handleLogout } = useLogout();
const route = useRoute();
const homeTimeline = useHomeTimelineStore();
const notificationStore = useNotificationStore();
const searchSession = useSearchSessionStore();
const currentProfileID = computed(() => {
  const id = authStore.currentIdentity?.id;

  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
});

const navigation = [
  { name: 'Home', label: 'Home', icon: 'home' as const, iconSize: 26, compactOnly: false },
  { name: 'UserSearch', label: 'Search', icon: 'search' as const, iconSize: 26, compactOnly: false },
  { name: 'Notifications', label: 'Notifications', icon: 'notifications' as const, iconSize: 26, compactOnly: false },
  { name: 'History', label: 'History', icon: 'history' as const, iconSize: 26, compactOnly: false },
  { name: 'CurrencyExchange', label: 'Exchange', icon: 'exchange' as const, iconSize: 26, compactOnly: true },
  { name: 'UserProfile', label: 'Profile', icon: 'profile' as const, iconSize: 26, compactOnly: false },
  { name: 'PostCreate', label: 'Post', icon: 'compose' as const, iconSize: 24, compactOnly: false, primary: true },
];

const homeDestination = computed(() => (
  authStore.isAuthenticated && homeTimeline.activeTab === 'following'
    ? { name: 'Home', query: { tab: 'following' } }
    : { name: 'Home' }
));

const searchReturnTarget = computed(() => {
  if (route.name === 'UserSearch' && typeof route.fullPath === 'string' && route.fullPath) {
    return route.fullPath;
  }

  const routeQuery = typeof route.query?.q === 'string' ? route.query.q.trim() : '';
  const sessionQuery = typeof searchSession.query === 'string' ? searchSession.query.trim() : '';
  const query = routeQuery || sessionQuery;
  return query ? `/search?q=${encodeURIComponent(query)}` : '/search';
});

const navigationDestination = (item: typeof navigation[number]) => {
  if (item.name === 'Home') {
    return homeDestination.value;
  }

  if (item.name === 'UserSearch') {
    return authStore.isAuthenticated
      ? { name: 'UserSearch' }
      : { name: 'Login', query: { returnTo: searchReturnTarget.value } };
  }

  if (item.name === 'Notifications') {
    return authStore.isAuthenticated
      ? { name: 'Notifications' }
      : { name: 'Login', query: { returnTo: '/notifications' } };
  }

  if (item.name === 'History') {
    return authStore.isAuthenticated
      ? { name: 'History' }
      : { name: 'Login', query: { returnTo: '/history' } };
  }

  if (item.name === 'UserProfile') {
    return authStore.isAuthenticated && currentProfileID.value !== null
      ? { name: 'UserProfile', params: { id: String(currentProfileID.value) } }
      : { name: 'Login', query: { intent: 'profile' } };
  }

  if (item.name === 'PostCreate') {
    return authStore.isAuthenticated
      ? { name: 'PostCreate' }
      : { name: 'Login', query: { returnTo: '/posts/new' } };
  }

  return { name: item.name };
};

const visibleNavigation = computed(() => navigation.filter((item) => (
  item.name !== 'UserProfile'
  || !authStore.isAuthenticated
  || currentProfileID.value !== null
)));

const isStandardActivation = (event: MouseEvent) => event.button === 0
  && !event.metaKey
  && !event.ctrlKey
  && !event.shiftKey
  && !event.altKey;

const handleNavigationClick = (
  event: MouseEvent,
  item: typeof navigation[number],
) => {
  if (!authStore.isAuthenticated || !isStandardActivation(event)) {
    return;
  }

  if (item.name === 'UserSearch' && route.name === 'UserSearch') {
    event.preventDefault();
    searchSession.requestSearchReselect();
    return;
  }

  if (item.name === 'Notifications' && route.name === 'Notifications') {
    event.preventDefault();
    notificationStore.requestNotificationReselect();
    return;
  }

  if (item.name !== 'Home' || route.name !== 'Home') {
    return;
  }

  event.preventDefault();
  homeTimeline.requestHomeReselect();
};

const notificationBadge = computed(() => (
  authStore.isAuthenticated ? props.notificationBadge : null
));

</script>

<style scoped>
.left-sidebar {
  display: flex;
  min-height: 100%;
  flex-direction: column;
  padding: var(--space-3) 0;
}

.left-sidebar__brand {
  display: inline-flex;
  align-items: center;
  min-height: 48px;
  padding: 0 var(--space-4);
  color: var(--color-text);
  font-size: 16px;
  font-weight: 750;
  text-decoration: none;
}

.left-sidebar__brand-mark {
  display: block;
  width: 34px;
  height: 34px;
}

.left-sidebar__nav {
  display: grid;
  gap: var(--space-1);
  margin-top: var(--space-5);
}

.left-sidebar__link {
  display: flex;
  align-items: center;
  min-height: 54px;
  gap: 16px;
  margin: 0 var(--space-2);
  padding: 0 var(--space-4);
  border: 0;
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
  font: inherit;
  font-size: 17px;
  font-weight: 620;
  text-align: left;
  text-decoration: none;
  transition: background-color var(--transition-fast), color var(--transition-fast), border-color var(--transition-fast);
}

.left-sidebar__link--compact-only {
  display: none;
}

.left-sidebar__link:hover,
.left-sidebar__link:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-text);
}

.left-sidebar__link.router-link-active {
  color: var(--color-text);
  font-weight: 750;
}

.left-sidebar__link.router-link-active .app-icon {
  color: var(--color-accent);
}

.left-sidebar__badge {
  display: inline-flex;
  min-width: 22px;
  height: 20px;
  align-items: center;
  justify-content: center;
  margin-left: auto;
  padding: 0 6px;
  border-radius: var(--radius-pill);
  background: var(--color-accent);
  color: var(--color-surface);
  font-size: 11px;
  font-weight: 800;
  line-height: 1;
}

.left-sidebar__link--primary {
  min-height: 52px;
  margin-top: var(--space-2);
  justify-content: center;
  background: var(--color-accent);
  color: var(--color-surface);
  font-weight: 750;
}

.left-sidebar__link--primary:hover,
.left-sidebar__link--primary:focus-visible {
  background: var(--color-accent-hover);
  color: var(--color-surface);
}

.left-sidebar__account {
  display: grid;
  gap: var(--space-1);
  margin-top: auto;
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border);
}

.left-sidebar__signup {
  color: var(--color-accent);
}

.left-sidebar__logout {
  width: auto;
}

@media (min-width: 800px) and (max-width: 1279px) {
  .left-sidebar__link--compact-only {
    display: flex;
  }
}
</style>
