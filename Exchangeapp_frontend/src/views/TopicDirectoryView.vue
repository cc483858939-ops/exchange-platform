<template>
  <main class="topic-directory-view">
    <header class="topic-directory-view__header">
      <h1>Topics</h1>
    </header>

    <section
      v-if="topicDirectory.status === 'idle' || topicDirectory.loading"
      class="topic-directory-view__state"
      role="status"
      aria-live="polite"
    >
      Loading topics…
    </section>

    <section v-else-if="topicDirectory.unavailable" class="topic-directory-view__state">
      <p role="alert">{{ topicDirectory.error }}</p>
      <button class="topic-directory-view__retry" type="button" @click="topicDirectory.retry()">Retry</button>
    </section>

    <section v-else-if="topicDirectory.items.length === 0" class="topic-directory-view__state">
      No topics available right now.
    </section>

    <nav v-else class="topic-directory-view__list" aria-label="Topics">
      <RouterLink
        v-for="topic in topicDirectory.items"
        :key="topic.slug"
        class="topic-directory-view__item"
        :to="{ name: 'Topic', params: { slug: topic.slug } }"
      >
        <span class="topic-directory-view__label">#{{ topic.label }}</span>
        <span class="topic-directory-view__description">{{ topic.description }}</span>
      </RouterLink>
    </nav>
  </main>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';
import { useTopicDirectoryStore } from '../store/topicDirectory';

const topicDirectory = useTopicDirectoryStore();

onMounted(() => { void topicDirectory.ensureLoaded(); });
</script>

<style scoped>
.topic-directory-view {
  min-height: 100%;
  background: var(--color-surface);
}

.topic-directory-view__header {
  display: flex;
  min-height: 56px;
  align-items: center;
  padding: 0 var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.topic-directory-view__header h1 {
  margin: 0;
  font-size: 21px;
  font-weight: 780;
  letter-spacing: -0.02em;
}

.topic-directory-view__state {
  display: grid;
  justify-items: center;
  gap: var(--space-3);
  padding: 56px var(--space-5);
  color: var(--color-text-secondary);
  text-align: center;
}

.topic-directory-view__state p {
  margin: 0;
}

.topic-directory-view__retry {
  min-height: 44px;
  padding: 0 var(--space-4);
  border: 0;
  border-radius: var(--radius-pill);
  background: var(--color-accent);
  color: var(--color-surface);
  cursor: pointer;
  font: inherit;
  font-weight: 700;
}

.topic-directory-view__retry:hover,
.topic-directory-view__retry:focus-visible {
  background: var(--color-accent-hover);
}

.topic-directory-view__list {
  display: grid;
}

.topic-directory-view__item {
  display: grid;
  min-height: 76px;
  align-content: center;
  gap: var(--space-1);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  text-decoration: none;
}

.topic-directory-view__label {
  color: var(--color-text);
  font-size: 16px;
  font-weight: 740;
}

.topic-directory-view__description {
  color: var(--color-text-secondary);
  font-size: 14px;
  line-height: 1.45;
}

.topic-directory-view__item:hover .topic-directory-view__label,
.topic-directory-view__item:focus-visible .topic-directory-view__label {
  color: var(--color-accent);
}

@media (max-width: 420px) {
  .topic-directory-view__header,
  .topic-directory-view__item {
    padding-inline: var(--space-4);
  }
}
</style>
