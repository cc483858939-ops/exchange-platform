<template>
  <div v-show="open" class="mention-suggestions" aria-live="polite">
    <div
      :id="listboxId"
      class="mention-suggestions__list"
      role="listbox"
      aria-label="Mention suggestions"
      :aria-busy="state === 'loading'"
    >
      <p v-if="state === 'loading'" class="mention-suggestions__status" role="status">
        Finding people…
      </p>
      <p v-else-if="state === 'error'" class="mention-suggestions__status" role="status">
        Suggestions are unavailable. You can keep typing.
      </p>
      <p v-else-if="items.length === 0" class="mention-suggestions__status" role="status">
        No matching people yet.
      </p>
      <button
        v-for="(item, index) in items"
        :id="optionId(item)"
        :key="item.id"
        type="button"
        class="mention-suggestions__option"
        role="option"
        :aria-selected="index === activeIndex"
        @mousedown.prevent
        @click="$emit('select', item)"
      >
        <img
          v-if="item.avatar_url"
          class="mention-suggestions__avatar"
          :src="item.avatar_url"
          alt=""
          loading="lazy"
        >
        <span v-else class="mention-suggestions__avatar mention-suggestions__avatar--fallback" aria-hidden="true">
          {{ (item.display_name || item.username).slice(0, 1).toUpperCase() }}
        </span>
        <span class="mention-suggestions__identity">
          <strong>{{ item.display_name || item.username }}</strong>
          <span>@{{ item.username }}</span>
        </span>
        <span v-if="item.reason === 'recently_mentioned'" class="mention-suggestions__reason">
          Recent
        </span>
      </button>
    </div>
    <RouterLink
      :id="`${listboxId}-search`"
      class="mention-suggestions__search"
      :to="searchTarget"
      @mousedown.prevent
    >
      Search more users
    </RouterLink>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { MentionSuggestion } from '../../services/mentionService';

const props = defineProps<{
  open: boolean;
  state: 'idle' | 'loading' | 'ready' | 'error';
  items: MentionSuggestion[];
  activeIndex: number;
  listboxId: string;
  prefix: string;
}>();

defineEmits<{
  select: [item: MentionSuggestion];
}>();

const searchTarget = computed(() => ({
  name: 'UserSearch',
  query: {
    tab: 'people',
    ...(props.prefix ? { q: props.prefix } : {}),
  },
}));

const optionId = (item: MentionSuggestion) => `${props.listboxId}-option-${item.id}`;
</script>

<style scoped>
.mention-suggestions {
  position: relative;
  z-index: 4;
  display: grid;
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  background: var(--color-surface);
  box-shadow: var(--shadow-md, 0 8px 28px rgb(0 0 0 / 14%));
}

.mention-suggestions__list {
  max-height: min(280px, 38dvh);
  overflow-y: auto;
  overscroll-behavior: contain;
}

.mention-suggestions__option {
  display: flex;
  width: 100%;
  min-height: 52px;
  align-items: center;
  gap: var(--space-3);
  border: 0;
  padding: var(--space-2) var(--space-3);
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  text-align: left;
}

.mention-suggestions__option[aria-selected="true"],
.mention-suggestions__option:focus-visible {
  outline: none;
  background: var(--color-surface-subtle);
}

.mention-suggestions__option:focus-visible {
  box-shadow: inset 0 0 0 2px var(--color-accent);
}

@media (hover: hover) and (pointer: fine) {
  .mention-suggestions__option:hover {
    background: var(--color-surface-subtle);
  }
}

.mention-suggestions__avatar {
  display: grid;
  width: 36px;
  height: 36px;
  flex: 0 0 auto;
  place-items: center;
  overflow: hidden;
  border-radius: 50%;
  background: var(--color-surface-muted, var(--color-surface-subtle));
  color: var(--color-text-secondary);
  object-fit: cover;
  font-weight: 700;
}

.mention-suggestions__identity {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.mention-suggestions__identity strong,
.mention-suggestions__identity span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mention-suggestions__identity strong {
  font-size: 13px;
}

.mention-suggestions__identity span,
.mention-suggestions__reason,
.mention-suggestions__status,
.mention-suggestions__search {
  color: var(--color-text-secondary);
  font-size: 12px;
}

.mention-suggestions__reason {
  margin-left: auto;
}

.mention-suggestions__status {
  margin: 0;
  padding: var(--space-3);
}

.mention-suggestions__search {
  display: flex;
  min-height: 44px;
  align-items: center;
  justify-content: center;
  border-top: 1px solid var(--color-border);
  color: var(--color-accent);
  text-decoration: none;
}

.mention-suggestions__search:hover,
.mention-suggestions__search:focus-visible {
  background: var(--color-surface-subtle);
  outline: 2px solid var(--color-accent);
  outline-offset: -2px;
}
</style>
