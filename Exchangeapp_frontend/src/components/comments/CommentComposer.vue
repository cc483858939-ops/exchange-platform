<template>
  <form class="comment-composer" @submit.prevent="submitReply">
    <div class="comment-composer__body">
      <span v-if="author" class="comment-composer__avatar" aria-hidden="true">
        {{ initial }}
      </span>
      <textarea
        ref="textareaRef"
        v-model="content"
        class="comment-composer__textarea"
        rows="1"
        maxlength="2000"
        :disabled="disabled || submitting"
        placeholder="Post your reply"
        aria-label="Reply content"
        @input="resizeTextarea"
      />
    </div>

    <div class="comment-composer__footer">
      <span class="comment-composer__hint">Keep it useful.</span>
      <button class="comment-composer__submit" type="submit" :disabled="disabled || submitting || !trimmedContent">
        {{ submitting ? 'Replying...' : 'Reply' }}
      </button>
    </div>
  </form>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue';
import type { PublicAuthor } from '../../types/User';

const props = withDefaults(defineProps<{
  author?: PublicAuthor | null;
  disabled?: boolean;
  submitting?: boolean;
}>(), {
  author: null,
  disabled: false,
  submitting: false,
});

const emit = defineEmits<{
  submit: [content: string];
}>();

const content = ref('');
const textareaRef = ref<HTMLTextAreaElement | null>(null);
const trimmedContent = computed(() => content.value.trim());
const initial = computed(() => Array.from(props.author?.username.trim() ?? '')[0]?.toUpperCase() || '?');

const resizeTextarea = () => {
  const textarea = textareaRef.value;
  if (!textarea) {
    return;
  }

  textarea.style.height = 'auto';
  textarea.style.height = Math.min(textarea.scrollHeight, 180) + 'px';
};

const clear = () => {
  content.value = '';
  void nextTick(resizeTextarea);
};

const submitReply = () => {
  if (props.disabled || props.submitting || !trimmedContent.value) {
    return;
  }

  emit('submit', trimmedContent.value);
};

defineExpose({ clear });

onMounted(resizeTextarea);
</script>

<style scoped>
.comment-composer {
  padding: var(--space-4) 0;
  border-bottom: 1px solid var(--color-border);
}

.comment-composer__body {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
}

.comment-composer__avatar {
  display: grid;
  width: 30px;
  height: 30px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid var(--color-border-strong);
  border-radius: 50%;
  background: var(--color-surface-subtle);
  color: var(--color-text-secondary);
  font-size: 12px;
  font-weight: 800;
}

.comment-composer__textarea {
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

.comment-composer__textarea::placeholder {
  color: var(--color-text-tertiary);
}

.comment-composer__textarea:focus {
  border-bottom: 1px solid var(--color-accent);
}

.comment-composer__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  margin-top: var(--space-3);
  padding-left: 42px;
}

.comment-composer__hint {
  color: var(--color-text-tertiary);
  font-size: 12px;
}

.comment-composer__submit {
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

.comment-composer__submit:hover:not(:disabled) {
  background: var(--color-accent-hover);
}

.comment-composer__submit:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

@media (max-width: 420px) {
  .comment-composer__footer {
    padding-left: 0;
  }

  .comment-composer__hint {
    display: none;
  }
}
</style>
