<template>
  <div class="feed-tabs" role="tablist" aria-label="Home feed">
    <button
      v-for="(tab, index) in tabs"
      :key="tab.value"
      :id="'feed-tab-' + tab.value"
      class="feed-tab"
      :class="{ 'feed-tab--active': activeTab === tab.value }"
      type="button"
      role="tab"
      :aria-selected="activeTab === tab.value"
      :aria-controls="'feed-panel-' + tab.value"
      :tabindex="activeTab === tab.value ? 0 : -1"
      :data-feed-tab="tab.value"
      @click="selectTab(tab.value)"
      @keydown="handleKeydown($event, index)"
    >
      {{ tab.label }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { nextTick } from 'vue';
import type { FeedTab } from '../../types/Feed';

defineProps<{
  activeTab: FeedTab;
}>();

const emit = defineEmits<{
  select: [tab: FeedTab];
}>();

const tabs: Array<{ value: FeedTab; label: string }> = [
  { value: 'for-you', label: 'For You' },
  { value: 'following', label: 'Following' },
];

const selectTab = (tab: FeedTab) => {
  emit('select', tab);
};

const handleKeydown = (event: KeyboardEvent, index: number) => {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) {
    return;
  }

  event.preventDefault();
  const nextIndex = event.key === 'Home'
    ? 0
    : event.key === 'End'
      ? tabs.length - 1
      : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
  const nextTab = tabs[nextIndex].value;
  emit('select', nextTab);
  void nextTick(() => {
    document.querySelector<HTMLElement>('[data-feed-tab="' + nextTab + '"]')?.focus();
  });
};
</script>

<style scoped>
.feed-tabs {
  display: inline-grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  min-width: min(100%, 232px);
  border-bottom: 1px solid var(--color-border);
}

.feed-tab {
  position: relative;
  min-height: 44px;
  border: 0;
  padding: 0 var(--space-4);
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
  font: inherit;
  font-size: 14px;
  font-weight: 700;
  white-space: nowrap;
}

.feed-tab::after {
  position: absolute;
  right: var(--space-4);
  bottom: -1px;
  left: var(--space-4);
  height: 2px;
  background: transparent;
  content: '';
}

.feed-tab:hover,
.feed-tab:focus-visible {
  color: var(--color-text);
}

.feed-tab--active {
  color: var(--color-text);
}

.feed-tab--active::after {
  background: var(--color-accent);
}

@media (max-width: 420px) {
  .feed-tabs {
    width: 100%;
  }

  .feed-tab {
    padding-inline: var(--space-2);
  }

  .feed-tab::after {
    right: var(--space-2);
    left: var(--space-2);
  }
}
</style>
