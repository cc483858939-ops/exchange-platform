<template>
  <Teleport to="body">
    <section
      v-if="open && !disabled"
      :id="id"
      ref="popoverRef"
      class="emoji-picker-popover__panel"
      role="dialog"
      aria-label="Emoji picker"
      :style="popoverStyle"
    >
      <p v-if="loadError" class="emoji-picker-popover__message" role="alert">
        {{ loadError }}
      </p>
      <p v-else-if="isLoading" class="emoji-picker-popover__message" aria-live="polite">
        Loading emojis…
      </p>
      <div ref="pickerMount" class="emoji-picker-popover__mount"></div>
    </section>
  </Teleport>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue';
import localEmojiDataSource from 'emoji-picker-element-data/en/emojibase/data.json?url';

export type EmojiPickerCloseReason = 'escape' | 'outside';

const props = withDefaults(defineProps<{
  open: boolean;
  anchorEl: HTMLElement | null;
  disabled?: boolean;
  id: string;
}>(), {
  disabled: false,
});

const emit = defineEmits<{
  select: [emoji: string];
  close: [reason: EmojiPickerCloseReason];
}>();

type EmojiPickerElement = HTMLElement & {
  addEventListener: HTMLElement['addEventListener'];
  removeEventListener: HTMLElement['removeEventListener'];
};

const popoverRef = ref<HTMLElement | null>(null);
const pickerMount = ref<HTMLElement | null>(null);
const pickerElement = ref<EmojiPickerElement | null>(null);
const popoverStyle = ref<Record<string, string>>({});
const isLoading = ref(false);
const loadError = ref('');
let lifecycleToken = 0;

const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max);

const isInsidePopover = (event: Event) => {
  const target = event.target;
  const path = typeof event.composedPath === 'function' ? event.composedPath() : [];
  const popover = popoverRef.value;
  const anchor = props.anchorEl;

  return Boolean(
    (popover && (path.includes(popover) || (target instanceof Node && popover.contains(target))))
    || (anchor && (path.includes(anchor) || (target instanceof Node && anchor.contains(target)))),
  );
};

const handleDocumentPointer = (event: Event) => {
  if (!isInsidePopover(event)) {
    emit('close', 'outside');
  }
};

const handleDocumentKeydown = (event: KeyboardEvent) => {
  if (event.key !== 'Escape') {
    return;
  }
  event.preventDefault();
  emit('close', 'escape');
};

const cleanupGlobalListeners = () => {
  document.removeEventListener('pointerdown', handleDocumentPointer, true);
  document.removeEventListener('click', handleDocumentPointer, true);
  document.removeEventListener('keydown', handleDocumentKeydown, true);
  window.removeEventListener('resize', positionPopover);
  window.removeEventListener('scroll', positionPopover, true);
};

const addGlobalListeners = () => {
  document.addEventListener('pointerdown', handleDocumentPointer, true);
  document.addEventListener('click', handleDocumentPointer, true);
  document.addEventListener('keydown', handleDocumentKeydown, true);
  window.addEventListener('resize', positionPopover);
  window.addEventListener('scroll', positionPopover, true);
};

const positionPopover = () => {
  const anchor = props.anchorEl;
  const popover = popoverRef.value;
  if (!anchor || !popover) {
    return;
  }

  const margin = 12;
  const gap = 8;
  const anchorRect = anchor.getBoundingClientRect();
  const popoverRect = popover.getBoundingClientRect();
  const viewportWidth = Math.max(window.innerWidth || 0, document.documentElement.clientWidth || 0);
  const viewportHeight = Math.max(window.innerHeight || 0, document.documentElement.clientHeight || 0);
  const width = popoverRect.width || Math.min(380, Math.max(0, viewportWidth - margin * 2));
  const height = popoverRect.height || Math.min(420, Math.max(0, viewportHeight - margin * 2));
  const maxLeft = Math.max(margin, viewportWidth - width - margin);
  const left = clamp(anchorRect.left, margin, maxLeft);
  const below = anchorRect.bottom + gap;
  const above = anchorRect.top - height - gap;
  const top = below + height <= viewportHeight - margin || above < margin
    ? clamp(below, margin, Math.max(margin, viewportHeight - height - margin))
    : clamp(above, margin, Math.max(margin, viewportHeight - height - margin));

  popoverStyle.value = {
    top: `${Math.round(top)}px`,
    left: `${Math.round(left)}px`,
  };
};

const handleEmojiClick = (event: Event) => {
  const detail = (event as CustomEvent<{
    unicode?: string;
    emoji?: { unicode?: string };
  }>).detail;
  const unicode = detail?.unicode || detail?.emoji?.unicode;
  if (unicode) {
    emit('select', unicode);
  }
};

const destroyPicker = () => {
  const picker = pickerElement.value;
  if (picker) {
    picker.removeEventListener('emoji-click', handleEmojiClick);
    picker.remove();
  }
  pickerElement.value = null;
  pickerMount.value?.replaceChildren();
};

const mountPicker = async (token: number) => {
  isLoading.value = true;
  loadError.value = '';
  try {
    const { Picker } = await import('emoji-picker-element');
    if (token !== lifecycleToken || !props.open || props.disabled || !pickerMount.value) {
      return;
    }
    const picker = new Picker({ dataSource: localEmojiDataSource }) as EmojiPickerElement;
    picker.addEventListener('emoji-click', handleEmojiClick);
    pickerMount.value.replaceChildren(picker);
    pickerElement.value = picker;
  } catch {
    if (token === lifecycleToken) {
      loadError.value = 'Emoji picker could not be loaded.';
    }
  } finally {
    if (token === lifecycleToken) {
      isLoading.value = false;
    }
  }
};

const activate = async () => {
  const token = ++lifecycleToken;
  cleanupGlobalListeners();
  addGlobalListeners();
  await nextTick();
  if (token !== lifecycleToken || !props.open || props.disabled) {
    return;
  }
  void mountPicker(token).then(async () => {
    if (token !== lifecycleToken || !props.open || props.disabled) {
      return;
    }
    await nextTick();
    positionPopover();
  });
  positionPopover();
};

const deactivate = () => {
  lifecycleToken += 1;
  cleanupGlobalListeners();
  destroyPicker();
  isLoading.value = false;
  loadError.value = '';
  popoverStyle.value = {};
};

watch(
  () => props.open && !props.disabled,
  active => {
    if (active) {
      void activate();
    } else {
      deactivate();
    }
  },
  { immediate: true },
);

watch(
  () => props.anchorEl,
  () => {
    if (props.open && !props.disabled) {
      void nextTick(positionPopover);
    }
  },
);

onBeforeUnmount(deactivate);
</script>

<style scoped>
.emoji-picker-popover__panel {
  position: fixed;
  z-index: 60;
  display: flex;
  width: min(380px, calc(100vw - 24px));
  height: min(420px, calc(100vh - 24px));
  max-height: calc(100vh - 24px);
  overflow: hidden;
  flex-direction: column;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  background: var(--color-surface);
  box-shadow: 0 18px 44px rgba(15, 20, 25, 0.18), 0 4px 12px rgba(15, 20, 25, 0.08);
}

.emoji-picker-popover__mount {
  min-height: 0;
  flex: 1;
}

.emoji-picker-popover__mount :deep(emoji-picker) {
  display: block;
  width: 100%;
  height: 100%;
  --background: var(--color-surface);
  --border-color: var(--color-border);
  --border-radius: var(--radius-md);
  --button-active-background: color-mix(in srgb, var(--color-accent) 14%, transparent);
  --button-hover-background: color-mix(in srgb, var(--color-accent) 8%, transparent);
  --category-font-color: var(--color-text);
  --indicator-color: var(--color-accent);
  --input-border-color: var(--color-border-strong);
  --input-font-color: var(--color-text);
  --input-placeholder-color: var(--color-text-tertiary);
  --outline-color: var(--color-accent);
}

.emoji-picker-popover__message {
  display: grid;
  min-height: 100%;
  margin: 0;
  place-items: center;
  padding: var(--space-5);
  color: var(--color-text-secondary);
  font-size: 14px;
  text-align: center;
}

@media (max-width: 600px) {
  .emoji-picker-popover__panel {
    width: min(360px, calc(100vw - 24px));
    height: min(420px, calc(100vh - 24px));
  }
}
</style>
