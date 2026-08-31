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
        @input="resizeTextarea"
      />
    </div>

    <div class="reply-composer__footer">
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
import { computed, nextTick, onMounted, ref, watch } from 'vue';
import type { PublicAuthor } from '../../types/User';
import UserAvatar from '../users/UserAvatar.vue';

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
const maxContentLength = 1000;
const content = computed({
  get: () => props.modelValue,
  set: value => emit('update:modelValue', value),
});
const trimmedContent = computed(() => content.value.trim());
const contentLength = computed(() => Array.from(trimmedContent.value).length);
const exceedsMaxLength = computed(() => contentLength.value > maxContentLength);

const resizeTextarea = () => {
  const textarea = textareaRef.value;
  if (!textarea) {
    return;
  }

  textarea.style.height = 'auto';
  textarea.style.height = Math.min(textarea.scrollHeight, 180) + 'px';
};

const clear = () => {
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
  if (props.disabled || props.submitting || !trimmedContent.value || exceedsMaxLength.value) {
    return;
  }

  emit('submit', trimmedContent.value);
};

defineExpose({ clear, focus });

onMounted(resizeTextarea);

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
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  margin-top: var(--space-3);
  padding-left: 42px;
}

.reply-composer__validation {
  font-size: 12px;
}

.reply-composer__validation {
  color: var(--color-danger);
}

.reply-composer__submit {
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


