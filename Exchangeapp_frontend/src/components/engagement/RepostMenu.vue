<template>
  <div
    ref="rootRef"
    class="repost-menu"
    :class="`repost-menu--${variant}`"
    @click.stop
  >
    <RepostAction
      ref="repostActionRef"
      :reposted="reposted"
      :count="count"
      :loading="loading"
      :pending="pending"
      :disabled="disabled"
      :ariaLabel="ariaLabel"
      :variant="variant"
      @toggle="emit('toggle')"
    />

    <button
      ref="disclosureRef"
      class="repost-menu__disclosure"
      type="button"
      aria-label="More repost options"
      aria-haspopup="menu"
      :aria-expanded="menuOpen"
      @click.stop="toggleMenu"
      @keydown.stop="handleDisclosureKeydown"
    >
      <AppIcon name="chevron-down" :size="16" />
    </button>

    <div
      v-if="menuOpen"
      ref="menuRef"
      class="repost-menu__items"
      role="menu"
      aria-label="Repost options"
      @click.stop
      @keydown.stop="handleMenuKeydown"
    >
      <button
        class="repost-menu__item"
        type="button"
        role="menuitem"
        :disabled="disabled || loading || pending"
        @click.stop="activateRepostFromMenu"
      >
        {{ repostMenuLabel }}
      </button>
      <button
        class="repost-menu__item"
        type="button"
        role="menuitem"
        :aria-label="quoteMenuLabel"
        @click.stop="selectQuote"
      >
        <span>Quote post</span>
        <span class="repost-menu__item-count" aria-hidden="true">
          {{ normalizedQuoteCount }}
        </span>
      </button>
      <button
        v-if="normalizedQuoteCount > 0"
        class="repost-menu__item"
        type="button"
        role="menuitem"
        aria-label="View quotes"
        @click.stop="selectViewQuotes"
      >
        View quotes
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import RepostAction from './RepostAction.vue';
import AppIcon from '../icons/AppIcon.vue';

type RepostMenuVariant = 'compact' | 'detail';
type RepostActionInstance = { activate: () => void };

const props = withDefaults(defineProps<{
  reposted: boolean;
  count: number;
  loading?: boolean;
  pending?: boolean;
  disabled?: boolean;
  quoteCount?: number;
  ariaLabel: string;
  variant?: RepostMenuVariant;
}>(), {
  loading: false,
  pending: false,
  disabled: false,
  quoteCount: 0,
  variant: 'compact',
});

const emit = defineEmits<{
  toggle: [];
  quote: [];
  viewQuotes: [];
}>();

const rootRef = ref<HTMLElement | null>(null);
const disclosureRef = ref<HTMLButtonElement | null>(null);
const menuRef = ref<HTMLDivElement | null>(null);
const repostActionRef = ref<RepostActionInstance | null>(null);
const menuOpen = ref(false);

const repostMenuLabel = computed(() => props.reposted ? 'Undo repost' : 'Repost');
const normalizedQuoteCount = computed(() => {
  const count = Number(props.quoteCount);
  return Number.isFinite(count) && Number.isInteger(count) && count >= 0 ? count : 0;
});
const quoteMenuLabel = computed(() => normalizedQuoteCount.value === 0
  ? 'Quote post, no existing quotes'
  : `Quote post, ${normalizedQuoteCount.value} existing quotes`);

const enabledMenuItems = () => Array.from(
  menuRef.value?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)') ?? [],
);

const focusMenuItem = (index: number) => {
  const items = enabledMenuItems();
  if (items.length === 0) return;
  items[Math.max(0, Math.min(index, items.length - 1))]?.focus();
};

const closeMenu = (restoreFocus = false) => {
  menuOpen.value = false;
  if (restoreFocus) {
    void nextTick(() => disclosureRef.value?.focus());
  }
};

const openMenu = (focusFirst = false) => {
  menuOpen.value = true;
  if (focusFirst) {
    void nextTick(() => focusMenuItem(0));
  }
};

const toggleMenu = () => {
  if (menuOpen.value) {
    closeMenu();
    return;
  }
  openMenu();
};

const handleDisclosureKeydown = (event: KeyboardEvent) => {
  if (event.key === 'ArrowDown') {
    event.preventDefault();
    if (!menuOpen.value) {
      openMenu(true);
    } else {
      focusMenuItem(0);
    }
    return;
  }

  if (event.key === 'Escape' && menuOpen.value) {
    event.preventDefault();
    closeMenu(true);
  }
};

const handleMenuKeydown = (event: KeyboardEvent) => {
  const items = enabledMenuItems();
  if (items.length === 0) return;

  if (event.key === 'Escape') {
    event.preventDefault();
    closeMenu(true);
    return;
  }

  const activeIndex = items.indexOf(document.activeElement as HTMLButtonElement);
  let targetIndex: number | null = null;

  if (event.key === 'ArrowDown') {
    targetIndex = activeIndex < 0 ? 0 : (activeIndex + 1) % items.length;
  } else if (event.key === 'ArrowUp') {
    targetIndex = activeIndex < 0 ? items.length - 1 : (activeIndex - 1 + items.length) % items.length;
  } else if (event.key === 'Home') {
    targetIndex = 0;
  } else if (event.key === 'End') {
    targetIndex = items.length - 1;
  }

  if (targetIndex !== null) {
    event.preventDefault();
    items[targetIndex]?.focus();
  }
};

const handleOutsidePointerDown = (event: PointerEvent) => {
  const target = event.target;
  if (target instanceof Node && !rootRef.value?.contains(target)) {
    closeMenu();
  }
};

watch(menuOpen, open => {
  if (open) {
    document.addEventListener('pointerdown', handleOutsidePointerDown);
  } else {
    document.removeEventListener('pointerdown', handleOutsidePointerDown);
  }
});

const activateRepostFromMenu = () => {
  closeMenu(true);
  repostActionRef.value?.activate();
};

const selectQuote = () => {
  closeMenu(true);
  emit('quote');
};

const selectViewQuotes = () => {
  closeMenu(true);
  emit('viewQuotes');
};

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', handleOutsidePointerDown);
});
</script>

<style scoped>
.repost-menu {
  position: relative;
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  max-width: 100%;
}

.repost-menu__disclosure {
  display: grid;
  width: 28px;
  min-width: 28px;
  min-height: 40px;
  flex: 0 0 28px;
  place-items: center;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
}

.repost-menu__disclosure:focus-visible,
.repost-menu__item:focus-visible {
  outline: 2px solid var(--color-repost);
  outline-offset: 2px;
}

@media (hover: hover) and (pointer: fine) {
  .repost-menu__disclosure:hover {
    color: var(--color-repost);
  }
}

.repost-menu__items {
  position: absolute;
  bottom: calc(100% + var(--space-1));
  left: 0;
  z-index: 20;
  min-width: 160px;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-sm);
  padding: var(--space-1);
  background: var(--color-surface);
  color: var(--color-text);
}

.repost-menu__item {
  display: flex;
  width: 100%;
  min-height: 40px;
  align-items: center;
  border: 0;
  border-radius: var(--radius-sm);
  padding: 0 var(--space-3);
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  text-align: left;
  white-space: nowrap;
}

.repost-menu__item-count {
  margin-left: auto;
  padding-left: var(--space-4);
  color: var(--color-text-tertiary);
  font-variant-numeric: tabular-nums;
}

@media (hover: hover) and (pointer: fine) {
  .repost-menu__item:hover:not(:disabled) {
    background: var(--color-surface-subtle);
  }
}

.repost-menu__item:disabled {
  color: var(--color-text-tertiary);
  cursor: default;
  opacity: 0.64;
}
</style>
