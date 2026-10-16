import { computed, onBeforeUnmount, ref, shallowRef, useId } from 'vue';
import { getMentionSuggestions, type MentionSuggestion } from '../services/mentionService';
import { mentionAtCaret, type MentionCaretContext } from '../utils/mentionText';

type UpdateOptions = {
  composing?: boolean;
  disabled?: boolean;
};

type SuggestionsState = 'idle' | 'loading' | 'ready' | 'error';

export function useMentionSuggestions() {
  const listboxId = useId();
  const isOpen = ref(false);
  const state = ref<SuggestionsState>('idle');
  const items = shallowRef<MentionSuggestion[]>([]);
  const activeIndex = ref(0);
  const prefix = ref('');
  const activeContext = shallowRef<MentionCaretContext | null>(null);
  const activeOptionId = computed(() => {
    const selected = items.value[activeIndex.value];
    return isOpen.value && selected ? `${listboxId}-option-${selected.id}` : undefined;
  });

  let lastContextKey = '';
  let timer: ReturnType<typeof setTimeout> | null = null;
  let controller: AbortController | null = null;
  let requestVersion = 0;

  const clearPendingRequest = () => {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
    controller?.abort();
    controller = null;
  };

  const close = () => {
    clearPendingRequest();
    requestVersion += 1;
    isOpen.value = false;
    state.value = 'idle';
    items.value = [];
    activeIndex.value = 0;
    prefix.value = '';
    activeContext.value = null;
    lastContextKey = '';
  };

  const update = (
    text: string,
    selectionStart: number,
    selectionEnd: number,
    options: UpdateOptions = {},
  ) => {
    if (options.disabled || options.composing) {
      close();
      return;
    }

    const context = mentionAtCaret(text, selectionStart, selectionEnd);
    if (!context) {
      close();
      return;
    }

    activeContext.value = context;
    prefix.value = context.prefix;
    const contextKey = `${context.start}:${context.prefix}`;
    if (isOpen.value && contextKey === lastContextKey) {
      return;
    }

    clearPendingRequest();
    const version = ++requestVersion;
    lastContextKey = contextKey;
    isOpen.value = true;
    state.value = 'loading';
    items.value = [];
    activeIndex.value = 0;

    timer = setTimeout(() => {
      timer = null;
      const nextController = new AbortController();
      controller = nextController;
      void getMentionSuggestions(context.prefix, nextController.signal)
        .then(result => {
          if (version !== requestVersion || nextController.signal.aborted) {
            return;
          }
          items.value = result.slice(0, 8);
          activeIndex.value = 0;
          state.value = 'ready';
        })
        .catch(() => {
          if (version !== requestVersion || nextController.signal.aborted) {
            return;
          }
          items.value = [];
          state.value = 'error';
        });
    }, 200);
  };

  const moveSelection = (delta: number) => {
    if (!isOpen.value || items.value.length === 0) {
      return;
    }
    activeIndex.value = (activeIndex.value + delta + items.value.length) % items.value.length;
  };

  const selectedItem = () => items.value[activeIndex.value] ?? null;
  onBeforeUnmount(close);

  return {
    listboxId,
    isOpen,
    state,
    items,
    activeIndex,
    activeOptionId,
    prefix,
    activeContext,
    update,
    close,
    moveSelection,
    selectedItem,
  };
}
