<template>
  <nav class="mobile-bottom-nav" aria-label="Mobile navigation">
    <div
      class="mobile-bottom-nav__items"
      :class="{ 'mobile-bottom-nav__items--anonymous': !authStore.isAuthenticated }"
    >
      <RouterLink
        v-for="item in navigationItems"
        :key="item.label"
        class="mobile-bottom-nav__item"
        :class="{ 'mobile-bottom-nav__item--active': isItemActive(item) }"
        :to="item.to"
        :aria-label="item.label"
        :title="item.label"
        :aria-current="isItemActive(item) ? 'page' : undefined"
        @click.capture="handleNavigationClick($event, item)"
      >
        <span class="mobile-bottom-nav__icon">
          <AppIcon :name="item.icon" :size="24" :filled="isItemActive(item)" />
          <span
            v-if="item.routeName === 'Notifications' && notificationBadge"
            class="mobile-bottom-nav__badge"
            aria-label="Unread notifications"
          >{{ notificationBadge }}</span>
        </span>
        <span class="mobile-bottom-nav__label">{{ item.label }}</span>
      </RouterLink>
    </div>
  </nav>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { useAuthStore } from '../../store/auth';
import { useHomeTimelineStore } from '../../store/homeTimeline';
import { useSearchSessionStore } from '../../store/searchSession';
import AppIcon from '../icons/AppIcon.vue';

type RouteParam = string | string[] | undefined;
type NavigationItem = {
  label: string;
  routeName: string;
  icon: 'home' | 'search' | 'exchange' | 'notifications' | 'profile';
  to: { name: string; params?: { id: string }; query?: { tab?: 'following'; q?: string } };
};

const props = withDefaults(defineProps<{
  notificationBadge?: string | null;
}>(), {
  notificationBadge: null,
});

const authStore = useAuthStore();
const homeTimeline = useHomeTimelineStore();
const searchSession = useSearchSessionStore();
const route = useRoute();

const currentProfileID = computed(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? String(id) : null;
});

const homeDestination = computed<NavigationItem['to']>(() =>
  homeTimeline.activeTab === 'following'
    ? { name: 'Home', query: { tab: 'following' } }
    : { name: 'Home' },
);

const searchDestination = computed<NavigationItem['to']>(() => (
  searchSession.query
    ? { name: 'UserSearch', query: { q: searchSession.query } }
    : { name: 'UserSearch' }
));

const navigationItems = computed<NavigationItem[]>(() => {
  if (!authStore.isAuthenticated) {
    return [
      { label: 'Home', routeName: 'Home', icon: 'home', to: homeDestination.value },
      { label: 'Exchange', routeName: 'CurrencyExchange', icon: 'exchange', to: { name: 'CurrencyExchange' } },
      { label: 'Log in', routeName: 'Login', icon: 'profile', to: { name: 'Login' } },
    ];
  }

  return [
    { label: 'Home', routeName: 'Home', icon: 'home', to: homeDestination.value },
    { label: 'Search', routeName: 'UserSearch', icon: 'search', to: searchDestination.value },
    { label: 'Exchange', routeName: 'CurrencyExchange', icon: 'exchange', to: { name: 'CurrencyExchange' } },
    { label: 'Notifications', routeName: 'Notifications', icon: 'notifications', to: { name: 'Notifications' } },
    {
      label: 'Profile',
      routeName: 'UserProfile',
      icon: 'profile',
      to: {
        name: 'UserProfile',
        params: { id: currentProfileID.value || '' },
      },
    },
  ];
});

const firstRouteParam = (value: RouteParam) => Array.isArray(value) ? value[0] || '' : value || '';

const isOwnProfileRoute = () => {
  const profileID = currentProfileID.value;
  if (!profileID) {
    return false;
  }

  const routeName = String(route.name || '');
  const isProfileSurface = routeName === 'UserProfile'
    || routeName === 'UserFollowing'
    || routeName === 'UserFollowers'
    || routeName === 'History';

  return isProfileSurface
    && (routeName === 'History' || firstRouteParam(route.params?.id) === profileID);
};

const isItemActive = (item: NavigationItem) => {
  if (item.routeName === 'UserProfile') {
    return isOwnProfileRoute();
  }
  return route.name === item.routeName;
};

const isReselectableRoot = (item: NavigationItem) => {
  const routeName = String(route.name || '');
  if (item.routeName === 'UserProfile') {
    const profileID = currentProfileID.value;
    return Boolean(profileID)
      && routeName === 'UserProfile'
      && firstRouteParam(route.params?.id) === profileID;
  }

  return (
    item.routeName === 'Home'
    || item.routeName === 'UserSearch'
    || item.routeName === 'CurrencyExchange'
    || item.routeName === 'Notifications'
  ) && routeName === item.routeName;
};

const isStandardActivation = (event: MouseEvent) => event.button === 0
  && !event.metaKey
  && !event.ctrlKey
  && !event.shiftKey
  && !event.altKey;

const prefersReducedMotion = () => typeof window !== 'undefined'
  && typeof window.matchMedia === 'function'
  && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

const scrollToTop = () => {
  if (typeof window === 'undefined' || typeof window.scrollTo !== 'function') {
    return;
  }

  window.scrollTo({
    top: 0,
    behavior: prefersReducedMotion() ? 'auto' : 'smooth',
  });
};

const handleNavigationClick = (event: MouseEvent, item: NavigationItem) => {
  if (!isStandardActivation(event) || !isReselectableRoot(item)) {
    return;
  }

  event.preventDefault();
  scrollToTop();
};

const notificationBadge = computed(() => props.notificationBadge);
</script>

<style scoped>
.mobile-bottom-nav {
  display: none;
}

@media (max-width: 799px) {
  .mobile-bottom-nav {
    position: fixed;
    right: 0;
    bottom: 0;
    left: 0;
    z-index: 40;
    display: block;
    padding-bottom: var(--mobile-safe-bottom);
    border-top: 1px solid var(--color-border);
    background: var(--color-surface);
  }

  .mobile-bottom-nav__items {
    display: grid;
    height: var(--mobile-bottom-nav-height);
    grid-template-columns: repeat(5, minmax(0, 1fr));
  }

  .mobile-bottom-nav__items--anonymous {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .mobile-bottom-nav__item {
    display: inline-flex;
    min-width: 0;
    min-height: 44px;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 2px;
    touch-action: manipulation;
    color: var(--color-text-secondary);
    font-size: 10px;
    font-weight: 700;
    line-height: 1.1;
    text-align: center;
    text-decoration: none;
    transition: color var(--transition-fast), background-color var(--transition-fast);
  }

  .mobile-bottom-nav__item:focus-visible {
    background: var(--color-surface-subtle);
    color: var(--color-text);
  }

  @media (hover: hover) and (pointer: fine) {
    .mobile-bottom-nav__item:hover {
      background: var(--color-surface-subtle);
      color: var(--color-text);
    }
  }

  .mobile-bottom-nav__item:active {
    background: var(--color-surface-subtle);
    transform: scale(0.98);
  }

  .mobile-bottom-nav__item--active {
    color: var(--color-accent);
  }

  .mobile-bottom-nav__icon {
    position: relative;
    display: inline-grid;
    min-width: 24px;
    min-height: 24px;
    place-items: center;
  }

  .mobile-bottom-nav__label {
    min-width: 0;
    max-width: 100%;
    overflow: hidden;
    padding-inline: 2px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .mobile-bottom-nav__badge {
    position: absolute;
    top: -4px;
    right: -9px;
    min-width: 16px;
    max-width: 30px;
    height: 16px;
    overflow: hidden;
    padding: 0 3px;
    border-radius: var(--radius-pill);
    background: var(--color-accent);
    color: var(--color-surface);
    font-size: 9px;
    font-weight: 800;
    line-height: 16px;
    text-align: center;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

@media (prefers-reduced-motion: reduce) {
  .mobile-bottom-nav__item {
    transition: none;
  }
}
</style>
