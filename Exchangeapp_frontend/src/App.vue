<template>
  <RouterView v-slot="{ Component }">
    <component v-if="route.meta.layout === 'auth'" :is="Component" />
    <AppShell v-else>
      <KeepAlive
        v-if="preserveReturnSurfaceCache"
        :include="['HomeView', 'UserProfileView']"
        :max="1"
      >
        <component :is="Component" />
      </KeepAlive>
      <component v-else :is="Component" />
    </AppShell>
  </RouterView>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import AppShell from './components/layout/AppShell.vue';
import { initializePostViewTelemetry } from './services/postViewTelemetry';
import { useAuthStore } from './store/auth';

const route = useRoute();
const authStore = useAuthStore();
const preserveReturnSurfaceCache = computed(() => (
  route.name === 'Home'
  || route.name === 'UserProfile'
  || route.name === 'PostDetail'
));

initializePostViewTelemetry(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && id > 0 ? id : null;
});
</script>
