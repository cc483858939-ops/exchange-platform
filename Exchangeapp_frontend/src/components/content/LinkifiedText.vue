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
      <button
        v-else-if="segment.type === 'mention'"
        class="linkified-text__mention"
        type="button"
        :aria-label="`View @${segment.username} profile`"
        :disabled="resolvingUsernames.has(segment.username)"
        @click.stop="openMention(segment.username)"
      >{{ segment.text }}</button>
      <RouterLink
        v-else-if="hasInternalTarget"
        class="linkified-text__internal"
        :to="internalTarget"
        @click.capture="handleInternalActivation"
      >{{ segment.text }}</RouterLink>
      <span v-else>{{ segment.text }}</span>
    </template>
    <span class="sr-only" role="status" aria-live="polite">{{ mentionStatus }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue';
import { useRouter, type RouteLocationRaw } from 'vue-router';
import { linkifyText } from '../../utils/linkifyText';
import { resolveMentionUsername } from '../../services/mentionService';

const props = defineProps<{
  text: string;
  to?: RouteLocationRaw;
}>();

const emit = defineEmits<{
  'internal-activate': [event: MouseEvent];
}>();

const router = useRouter();
const segments = computed(() => linkifyText(props.text));
const hasInternalTarget = computed(() => props.to !== undefined);
const internalTarget = computed<RouteLocationRaw>(() => props.to!);
const resolvingUsernames = ref(new Set<string>());
const mentionStatus = ref('');
const resolutionControllers = new Map<string, AbortController>();

const openMention = async (username: string) => {
  if (resolvingUsernames.value.has(username)) {
    return;
  }
  const controller = new AbortController();
  resolutionControllers.set(username, controller);
  resolvingUsernames.value = new Set(resolvingUsernames.value).add(username);
  mentionStatus.value = '';
  try {
    const user = await resolveMentionUsername(username, controller.signal);
    if (controller.signal.aborted) {
      return;
    }
    if (!user) {
      mentionStatus.value = `Couldn’t find an active account for @${username}.`;
      return;
    }
    await router.push({ name: 'UserProfile', params: { id: String(user.id) } });
  } catch {
    if (!controller.signal.aborted) {
      mentionStatus.value = `Couldn’t open @${username}. Try again.`;
    }
  } finally {
    resolutionControllers.delete(username);
    const next = new Set(resolvingUsernames.value);
    next.delete(username);
    resolvingUsernames.value = next;
  }
};

onBeforeUnmount(() => {
  for (const controller of resolutionControllers.values()) {
    controller.abort();
  }
  resolutionControllers.clear();
});

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

.linkified-text__mention {
  display: inline;
  border: 0;
  border-radius: var(--radius-sm);
  padding: 0;
  background: transparent;
  color: var(--color-accent);
  cursor: pointer;
  font: inherit;
}

.linkified-text__mention:hover {
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

.linkified-text__mention:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}
</style>
