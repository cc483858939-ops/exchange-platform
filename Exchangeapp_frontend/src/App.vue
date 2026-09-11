<template>
  <RouterView v-slot="{ Component }">
    <component v-if="route.meta.layout === 'auth'" :is="Component" />
    <AppShell v-else>
      <KeepAlive
        :key="`root:${viewerCacheNamespace}`"
        :max="5"
      >
        <component
          v-if="rootSurfaceCacheKey"
          :is="Component"
          :key="rootSurfaceCacheKey"
        />
      </KeepAlive>
      <KeepAlive
        v-if="preserveExternalProfileCache"
        :key="`external:${viewerCacheNamespace}`"
        :max="1"
      >
        <component
          v-if="externalProfileCacheKey"
          :is="Component"
          :key="externalProfileCacheKey"
        />
      </KeepAlive>
      <component
        v-if="!rootSurfaceCacheKey && !externalProfileCacheKey"
        :is="Component"
      />
    </AppShell>
  </RouterView>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import AppShell from './components/layout/AppShell.vue';
import {
  getExternalProfileCacheKey,
  getRootSurfaceCacheKey,
  getViewerCacheNamespace,
  shouldPreserveExternalProfileCache,
} from './router/surfaceCachePolicy';
import { initializePostViewTelemetry } from './services/postViewTelemetry';
import { useAuthStore } from './store/auth';

const route = useRoute();
const authStore = useAuthStore();
const currentViewerID = computed(() => {
  const id = authStore.currentIdentity?.id;
  return authStore.isAuthenticated
    && typeof id === 'number'
    && Number.isSafeInteger(id)
    && id > 0
    ? id
    : null;
});
const viewerCacheNamespace = computed(() => getViewerCacheNamespace(currentViewerID.value));
const rootSurfaceCacheKey = computed(() => getRootSurfaceCacheKey(route, currentViewerID.value));
const externalProfileCacheKey = computed(() => getExternalProfileCacheKey(route, currentViewerID.value));
const preserveExternalProfileCache = computed(() => shouldPreserveExternalProfileCache(route));

initializePostViewTelemetry(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && id > 0 ? id : null;
});
</script>
