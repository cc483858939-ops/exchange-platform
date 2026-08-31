<template>
  <RouterView v-slot="{ Component }">
    <component v-if="route.meta.layout === 'auth'" :is="Component" />
    <AppShell v-else>
      <component :is="Component" />
    </AppShell>
  </RouterView>
</template>

<script setup lang="ts">
import { useRoute } from 'vue-router';
import AppShell from './components/layout/AppShell.vue';
import { initializePostViewTelemetry } from './services/postViewTelemetry';
import { useAuthStore } from './store/auth';

const route = useRoute();
const authStore = useAuthStore();

initializePostViewTelemetry(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && id > 0 ? id : null;
});
</script>
