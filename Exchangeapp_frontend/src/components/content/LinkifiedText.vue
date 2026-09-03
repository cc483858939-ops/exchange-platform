<template>
  <span class="linkified-text">
    <template v-for="(segment, index) in segments" :key="`${segment.type}-${index}`">
      <a
        v-if="segment.type === 'link'"
        class="linkified-text__external"
        :href="segment.href"
        target="_blank"
        rel="noopener noreferrer"
        @click.stop
      >{{ segment.text }}</a>
      <RouterLink
        v-else-if="hasInternalTarget"
        class="linkified-text__internal"
        :to="internalTarget"
        @click.capture="handleInternalActivation"
      >{{ segment.text }}</RouterLink>
      <span v-else>{{ segment.text }}</span>
    </template>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { RouteLocationRaw } from 'vue-router';
import { linkifyText } from '../../utils/linkifyText';

const props = defineProps<{
  text: string;
  to?: RouteLocationRaw;
}>();

const emit = defineEmits<{
  'internal-activate': [event: MouseEvent];
}>();

const segments = computed(() => linkifyText(props.text));
const hasInternalTarget = computed(() => props.to !== undefined);
const internalTarget = computed<RouteLocationRaw>(() => props.to!);

const handleInternalActivation = (event: MouseEvent) => {
  emit('internal-activate', event);
};
</script>

<style scoped>
.linkified-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.linkified-text__external {
  color: var(--color-accent);
  text-decoration: none;
}

.linkified-text__external:hover {
  text-decoration: underline;
}

.linkified-text__external:focus-visible,
.linkified-text__internal:focus-visible {
  border-radius: var(--radius-sm);
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

.linkified-text__internal {
  color: inherit;
  text-decoration: none;
}
</style>
