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

const route = useRoute();
</script>
