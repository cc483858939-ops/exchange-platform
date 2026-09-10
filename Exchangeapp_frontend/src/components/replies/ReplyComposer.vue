<template>
  <form class="reply-composer" @submit.prevent="submitReply">
    <div class="reply-composer__body">
      <UserAvatar
        v-if="author"
        class="reply-composer__avatar"
        :avatar-url="author.avatar_url"
        :display-name="author.display_name"
        :username="author.username"
        :size="30"
        decorative
      />
      <textarea
        ref="textareaRef"
        v-model="content"
        class="reply-composer__textarea"
        rows="1"
        :disabled="disabled || submitting"
        placeholder="Post your reply..."
        aria-label="Reply content"
        @input="handleTextareaInput"
        @select="syncTextareaSelection"
        @click="syncTextareaSelection"
        @keyup="syncTextareaSelection"
        @focus="syncTextareaSelection"
      />
    </div>

    <div class="reply-composer__footer">
      <div class="reply-composer__tools">
        <button
          ref="emojiButton"
          class="reply-composer__emoji"
          type="button"
          aria-label="Add emoji"
          title="Add emoji"
          aria-haspopup="dialog"
          :aria-expanded="emojiPickerOpen"
          :aria-controls="emojiPickerId"
          :disabled="emojiPickerDisabled"
          @click="toggleEmojiPicker"
        >
          <AppIcon name="smile" :size="18" />
        </button>
        <EmojiPickerPopover
          :id="emojiPickerId"
          :open="emojiPickerOpen"
          :anchor-el="emojiButton"
          :disabled="emojiPickerDisabled"
          @select="insertEmoji"
          @close="handleEmojiPickerClose"
        />
      </div>
      <span v-if="exceedsMaxLength" class="reply-composer__validation" role="alert">
        {{ contentLength }}/{{ maxContentLength }} characters. Please shorten your reply.
      </span>
      <button
        class="reply-composer__submit"
        type="submit"
        :disabled="disabled || submitting || !trimmedContent || exceedsMaxLength"
      >
        {{ submitting ? 'Replying...' : 'Reply' }}
      </button>
    </div>
  </form>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { PublicAuthor } from '../../types/User';
import EmojiPickerPopover, {
  type EmojiPickerCloseReason,
} from '../composer/EmojiPickerPopover.vue';
import AppIcon from '../icons/AppIcon.vue';
import UserAvatar from '../users/UserAvatar.vue';
import { insertTextAtSelection } from '../../utils/textareaInsertion';

const props = withDefaults(defineProps<{
  author?: PublicAuthor | null;
  disabled?: boolean;
  modelValue?: string;
  submitting?: boolean;
}>(), {
  author: null,
  disabled: false,
  modelValue: '',
  submitting: false,
});

const emit = defineEmits<{
  'update:modelValue': [value: string];
  submit: [content: string];
}>();

const textareaRef = ref<HTMLTextAreaElement | null>(null);
const emojiButton = ref<HTMLButtonElement | null>(null);
const emojiPickerOpen = ref(false);
const selectionStart = ref<number | null>(null);
const selectionEnd = ref<number | null>(null);
const emojiPickerId = 'reply-emoji-picker';
const maxContentLength = 1000;
const content = computed({
  get: () => props.modelValue,
  set: value => emit('update:modelValue', value),
});
const trimmedContent = computed(() => content.value.trim());
const contentLength = computed(() => Array.from(trimmedContent.value).length);
const exceedsMaxLength = computed(() => contentLength.value > maxContentLength);
const emojiPickerDisabled = computed(() => props.disabled || props.submitting);

const syncTextareaSelection = () => {
  const textarea = textareaRef.value;
  if (!textarea) {
    return;
  }
  selectionStart.value = textarea.selectionStart;
  selectionEnd.value = textarea.selectionEnd;
};

const focusTextareaAtSelection = async () => {
  await nextTick();
  const textarea = textareaRef.value;
  if (!textarea || textarea.disabled) {
    return;
  }
  const start = selectionStart.value ?? textarea.value.length;
  const end = selectionEnd.value ?? start;
  textarea.focus({ preventScroll: true });
  textarea.setSelectionRange(start, end);
  selectionStart.value = start;
  selectionEnd.value = end;
};

const handleEmojiPickerClose = (reason: EmojiPickerCloseReason) => {
  emojiPickerOpen.value = false;
  if (reason === 'escape') {
    void focusTextareaAtSelection();
  }
};

const toggleEmojiPicker = () => {
  if (emojiPickerDisabled.value) {
    return;
  }
  syncTextareaSelection();
  emojiPickerOpen.value = !emojiPickerOpen.value;
};

const insertEmoji = async (emoji: string) => {
  if (emojiPickerDisabled.value) {
    return;
  }
  const result = insertTextAtSelection(
    content.value,
    emoji,
    selectionStart.value,
    selectionEnd.value,
  );
  content.value = result.value;
  selectionStart.value = result.caret;
  selectionEnd.value = result.caret;
  await nextTick();
  const textarea = textareaRef.value;
  if (!textarea || textarea.disabled) {
    return;
  }
  textarea.focus({ preventScroll: true });
  textarea.setSelectionRange(result.caret, result.caret);
  selectionStart.value = result.caret;
  selectionEnd.value = result.caret;
  resizeTextarea();
};

const handleTextareaInput = () => {
  syncTextareaSelection();
  resizeTextarea();
};

const resizeTextarea = () => {
  const textarea = textareaRef.value;
  if (!textarea) {
    return;
  }

  textarea.style.height = 'auto';
  textarea.style.height = Math.min(textarea.scrollHeight, 180) + 'px';
};

const clear = () => {
  emojiPickerOpen.value = false;
  emit('update:modelValue', '');
  void nextTick(resizeTextarea);
};

const focus = async (): Promise<boolean> => {
  await nextTick();

  const textarea = textareaRef.value;
  if (!textarea || textarea.disabled) {
    return false;
  }

  textarea.scrollIntoView({
    behavior: 'auto',
    block: 'center',
  });
  textarea.focus({ preventScroll: true });
  return document.activeElement === textarea;
};

const submitReply = () => {
  emojiPickerOpen.value = false;
  if (props.disabled || props.submitting || !trimmedContent.value || exceedsMaxLength.value) {
    return;
  }

  emit('submit', trimmedContent.value);
};

defineExpose({ clear, focus });

onMounted(resizeTextarea);

watch(emojiPickerDisabled, disabled => {
  if (disabled) {
    emojiPickerOpen.value = false;
  }
});

onBeforeUnmount(() => {
  emojiPickerOpen.value = false;
});

watch(
  () => props.modelValue,
  () => {
    void nextTick(resizeTextarea);
  },
);
</script>

<style scoped>
.reply-composer {
  padding: var(--space-4) 0;
}

.reply-composer__body {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
}

.reply-composer__avatar {
  display: grid;
  width: 30px;
  height: 30px;
  flex: 0 0 auto;
  place-items: center;
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: 50%;
  background: var(--color-surface-subtle);
  color: var(--color-text-secondary);
  font-size: 12px;
  font-weight: 800;
}

.reply-composer__avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.reply-composer__textarea {
  width: 100%;
  min-height: 32px;
  max-height: 180px;
  resize: none;
  border: 0;
  padding: 5px 0;
  outline: 0;
  background: transparent;
  color: var(--color-text);
  line-height: 1.55;
}

.reply-composer__textarea::placeholder {
  color: var(--color-text-tertiary);
}

.reply-composer__textarea:focus {
  border-bottom: 1px solid var(--color-accent);
}

.reply-composer__footer {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-3);
  margin-top: var(--space-3);
  padding-left: 42px;
}

.reply-composer__tools {
  grid-column: 1;
}

.reply-composer__emoji {
  display: grid;
  width: 34px;
  height: 34px;
  place-items: center;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0;
  background: transparent;
  color: var(--color-accent);
  cursor: pointer;
}

.reply-composer__emoji:hover:not(:disabled),
.reply-composer__emoji:focus-visible:not(:disabled) {
  background: color-mix(in srgb, var(--color-accent) 10%, transparent);
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

.reply-composer__emoji:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.reply-composer__validation {
  grid-column: 2;
  min-width: 0;
  font-size: 12px;
}

.reply-composer__validation {
  color: var(--color-danger);
}

.reply-composer__submit {
  grid-column: 3;
  min-height: 34px;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-4);
  background: var(--color-accent);
  color: #fff;
  cursor: pointer;
  font-size: 13px;
  font-weight: 750;
  transition: background var(--transition-fast), opacity var(--transition-fast);
}

.reply-composer__submit:hover:not(:disabled) {
  background: var(--color-accent-hover);
}

.reply-composer__submit:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

@media (max-width: 420px) {
  .reply-composer__footer {
    padding-left: 0;
  }
}
</style>
