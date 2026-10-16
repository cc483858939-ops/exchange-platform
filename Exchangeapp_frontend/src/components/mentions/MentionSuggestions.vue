<template>
  <div v-show="open" class="mention-suggestions" aria-live="polite" @keydown.esc.prevent.stop="closeSearch">
    <template v-if="searchMode">
      <div class="mention-suggestions__search-form" role="search">
        <label class="sr-only" :for="`${listboxId}-search-input`">Search all people</label>
        <input
          :id="`${listboxId}-search-input`"
          ref="searchInput"
          v-model="searchQuery"
          class="mention-suggestions__search-input"
          type="search"
          autocomplete="off"
          placeholder="Search people..."
          @keydown.enter.prevent="submitSearch"
        >
        <button
          class="mention-suggestions__search-submit"
          type="button"
          :disabled="searchState === 'loading' || !searchQuery.trim() || !searchContextValid"
          @click="submitSearch"
        >Search</button>
      </div>
      <div
        :id="listboxId"
        class="mention-suggestions__list"
        role="listbox"
        aria-label="Search results"
        :aria-busy="searchState === 'loading'"
      >
        <p v-if="!searchContextValid || searchState === 'stale'" class="mention-suggestions__status" role="status">
          The draft changed. Close this search and start again from the current mention.
        </p>
        <p v-else-if="searchState === 'loading'" class="mention-suggestions__status" role="status">
          Searching people…
        </p>
        <p v-else-if="searchState === 'error'" class="mention-suggestions__status" role="alert">
          Search is unavailable. You can retry or return to your draft.
        </p>
        <p v-else-if="searchState === 'ready' && searchItems.length === 0" class="mention-suggestions__status" role="status">
          No matching people found.
        </p>
        <button
          v-for="item in searchItems"
          :id="optionId(item)"
          :key="item.id"
          type="button"
          class="mention-suggestions__option"
          role="option"
          :aria-selected="false"
          :disabled="!searchContextValid"
          @mousedown.prevent
          @click="selectSearchUser(item)"
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
        </button>
      </div>
      <div class="mention-suggestions__search-actions">
        <button ref="searchLink" class="mention-suggestions__search" type="button" @click="returnToSuggestions">
          Back to suggestions
        </button>
        <button class="mention-suggestions__search mention-suggestions__search--quiet" type="button" @click="closeSearch">
          Close
        </button>
      </div>
    </template>
    <template v-else>
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
      <button
        :id="`${listboxId}-search`"
        class="mention-suggestions__search"
        type="button"
        @mousedown.prevent
        @click="enterSearchMode"
      >
        Search more users
      </button>
    </template>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { searchUsers } from '../../services/userService';
import type { MentionSuggestion } from '../../services/mentionService';
import type { MentionCaretContext } from '../../utils/mentionText';

type MentionOption = Pick<MentionSuggestion, 'id' | 'username' | 'display_name' | 'avatar_url'> & {
  reason?: MentionSuggestion['reason'];
};
type SearchState = 'idle' | 'loading' | 'ready' | 'error' | 'stale';
type SearchContextSnapshot = {
  editorIdentity: string;
  editorText: string;
  mentionKey: string;
};

const props = defineProps<{
  open: boolean;
  state: 'idle' | 'loading' | 'ready' | 'error';
  items: MentionSuggestion[];
  activeIndex: number;
  listboxId: string;
  prefix: string;
  editorIdentity: string;
  editorText: string;
  activeContext: MentionCaretContext | null;
}>();

const emit = defineEmits<{
  select: [item: MentionOption];
  close: [];
}>();

const searchInput = ref<HTMLInputElement | null>(null);
const searchLink = ref<HTMLButtonElement | null>(null);
const searchMode = ref(false);
const searchQuery = ref('');
const searchState = ref<SearchState>('idle');
const searchItems = ref<MentionOption[]>([]);
const searchContextValid = ref(false);
let contextSnapshot: SearchContextSnapshot | null = null;
let searchController: AbortController | null = null;
let searchVersion = 0;

const mentionKey = (context: MentionCaretContext | null) => (
  context ? `${context.start}:${context.end}:${context.username}:${context.prefix}` : ''
);
const optionId = (item: MentionOption) => `${props.listboxId}-option-${item.id}`;

const cancelSearchRequest = () => {
  searchVersion += 1;
  searchController?.abort();
  searchController = null;
};

const resetSearch = () => {
  cancelSearchRequest();
  searchMode.value = false;
  searchQuery.value = '';
  searchState.value = 'idle';
  searchItems.value = [];
  contextSnapshot = null;
  searchContextValid.value = false;
};

const enterSearchMode = async () => {
  if (!props.activeContext) return;
  cancelSearchRequest();
  contextSnapshot = {
    editorIdentity: props.editorIdentity,
    editorText: props.editorText,
    mentionKey: mentionKey(props.activeContext),
  };
  searchContextValid.value = true;
  searchMode.value = true;
  searchQuery.value = props.prefix;
  searchState.value = 'idle';
  searchItems.value = [];
  await nextTick();
  searchInput.value?.focus();
};

const returnToSuggestions = async () => {
  resetSearch();
  await nextTick();
  searchLink.value?.focus();
};

const closeSearch = () => {
  resetSearch();
  emit('close');
};

const invalidateSearchContext = () => {
  if (!searchMode.value || !contextSnapshot || !searchContextValid.value) return;
  const stillCurrent = contextSnapshot.editorIdentity === props.editorIdentity
    && contextSnapshot.editorText === props.editorText
    && contextSnapshot.mentionKey === mentionKey(props.activeContext);
  if (stillCurrent) return;
  searchContextValid.value = false;
  cancelSearchRequest();
  searchItems.value = [];
  searchState.value = 'stale';
};

watch(
  [() => props.open, () => props.editorIdentity, () => props.editorText, () => mentionKey(props.activeContext)],
  ([open]) => {
    if (!open) resetSearch();
    else invalidateSearchContext();
  },
);

watch(searchQuery, () => {
  if (!searchMode.value || searchState.value === 'idle') return;
  cancelSearchRequest();
  searchItems.value = [];
  searchState.value = 'idle';
});

const submitSearch = async () => {
  const query = searchQuery.value.trim();
  if (!query || !searchContextValid.value) return;
  cancelSearchRequest();
  const version = searchVersion;
  const controller = new AbortController();
  searchController = controller;
  searchState.value = 'loading';
  searchItems.value = [];
  try {
    const page = await searchUsers({ q: query, limit: 8 }, controller.signal);
    if (version !== searchVersion || controller.signal.aborted || !searchContextValid.value) return;
    const seen = new Set<number>();
    searchItems.value = page.items.flatMap(({ user }) => {
      if (seen.has(user.id)) return [];
      seen.add(user.id);
      return [{
        id: user.id,
        username: user.username,
        display_name: user.display_name,
        avatar_url: user.avatar_url,
      }];
    }).slice(0, 8);
    searchState.value = 'ready';
  } catch {
    if (version === searchVersion && !controller.signal.aborted) {
      searchState.value = 'error';
    }
  } finally {
    if (searchController === controller) searchController = null;
  }
};

const selectSearchUser = (item: MentionOption) => {
  invalidateSearchContext();
  if (!searchContextValid.value || searchState.value !== 'ready') return;
  emit('select', item);
  resetSearch();
};

onBeforeUnmount(resetSearch);
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

.mention-suggestions__option:disabled { cursor: not-allowed; opacity: 0.55; }

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

.mention-suggestions__search-form {
  display: flex;
  gap: var(--space-2);
  padding: var(--space-2);
  border-bottom: 1px solid var(--color-border);
}

.mention-suggestions__search-input {
  width: 100%;
  min-width: 0;
  min-height: 40px;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  padding: 0 var(--space-3);
  background: var(--color-surface);
  color: var(--color-text);
  font: inherit;
}

.mention-suggestions__search-submit {
  min-height: 40px;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-4);
  background: var(--color-accent);
  color: var(--color-on-accent, #fff);
  cursor: pointer;
  font: inherit;
  font-weight: 700;
}

.mention-suggestions__search-submit:disabled { cursor: wait; opacity: 0.55; }
.mention-suggestions__search-input:focus-visible,
.mention-suggestions__search-submit:focus-visible,
.mention-suggestions__search:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

.mention-suggestions__search-actions { display: flex; border-top: 1px solid var(--color-border); }
.mention-suggestions__search {
  display: flex;
  width: 100%;
  min-height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  color: var(--color-accent);
  cursor: pointer;
  text-decoration: none;
}
.mention-suggestions__search--quiet { border-left: 1px solid var(--color-border); color: var(--color-text-secondary); }
.mention-suggestions__search:hover { background: var(--color-surface-subtle); }
</style>
