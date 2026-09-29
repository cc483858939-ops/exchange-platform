<template>
  <aside class="right-rail" aria-label="Explore and market quick access">
    <section class="right-rail__section" aria-labelledby="right-rail-topics-heading">
      <p class="right-rail__eyebrow">EXPLORE</p>
      <h2 id="right-rail-topics-heading">Topics</h2>
      <p v-if="topicDirectory.status === 'idle' || topicDirectory.loading" class="right-rail__status" role="status" aria-live="polite">
        Loading topics…
      </p>
      <div v-else-if="topicDirectory.unavailable" class="right-rail__topic-error">
        <p class="right-rail__status" role="alert">{{ topicDirectory.error }}</p>
        <button class="right-rail__retry" type="button" @click="topicDirectory.retry()">Retry</button>
      </div>
      <p v-else-if="topicDirectory.items.length === 0" class="right-rail__status">
        No topics available.
      </p>
      <nav v-else class="right-rail__topics" aria-label="Topics">
        <RouterLink
          v-for="topic in topicDirectory.items"
          :key="topic.slug"
          class="right-rail__topic"
          :to="{ name: 'Topic', params: { slug: topic.slug } }"
          :replace="route.name === 'Topic'"
        >
          <span class="right-rail__topic-label">#{{ topic.label }}</span>
          <span class="right-rail__topic-description">{{ topic.description }}</span>
        </RouterLink>
      </nav>
    </section>

    <section class="right-rail__section right-rail__section--market" aria-labelledby="right-rail-market-heading">
      <p class="right-rail__eyebrow">MARKET</p>
      <h2 id="right-rail-market-heading">Tools</h2>
      <nav class="right-rail__links" aria-label="Market quick access links">
        <RouterLink :to="{ name: 'CurrencyExchange' }">Exchange</RouterLink>
      </nav>
    </section>
  </aside>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';
import { useRoute } from 'vue-router';
import { useTopicDirectoryStore } from '../../store/topicDirectory';

const route = useRoute();
const topicDirectory = useTopicDirectoryStore();

onMounted(() => { void topicDirectory.ensureLoaded(); });
</script>

<style scoped>
.right-rail {
  margin-top: var(--space-5);
  padding: var(--space-5);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface);
}

.right-rail__section--market {
  margin-top: var(--space-5);
  padding-top: var(--space-5);
  border-top: 1px solid var(--color-border);
}

.right-rail__eyebrow {
  margin: 0 0 var(--space-2);
  color: var(--color-text-tertiary);
  font-size: 11px;
  font-weight: 750;
  letter-spacing: 0.12em;
}

h2 {
  margin: 0;
  color: var(--color-text);
  font-size: 18px;
  letter-spacing: -0.01em;
}

.right-rail__topics {
  display: grid;
  margin-top: var(--space-3);
}

.right-rail__topic {
  display: grid;
  gap: 3px;
  min-height: 52px;
  align-content: center;
  padding: var(--space-2) 0;
  border-bottom: 1px solid var(--color-border);
  text-decoration: none;
}

.right-rail__topic:last-child {
  border-bottom: 0;
}

.right-rail__topic-label {
  color: var(--color-text);
  font-size: 14px;
  font-weight: 700;
}

.right-rail__topic-description,
.right-rail__status {
  margin: 0;
  color: var(--color-text-tertiary);
  font-size: 12px;
  line-height: 1.4;
}

.right-rail__topic:hover .right-rail__topic-label,
.right-rail__topic:focus-visible .right-rail__topic-label {
  color: var(--color-accent);
}

.right-rail__status {
  margin-top: var(--space-3);
}

.right-rail__topic-error {
  margin-top: var(--space-3);
}

.right-rail__retry {
  min-height: 36px;
  margin-top: var(--space-2);
  padding: 0 var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-pill);
  background: var(--color-surface);
  color: var(--color-text);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
  font-weight: 700;
}

.right-rail__retry:hover,
.right-rail__retry:focus-visible {
  border-color: var(--color-accent);
  color: var(--color-accent);
}

.right-rail__links {
  display: grid;
  margin-top: var(--space-4);
}

.right-rail__links a {
  min-height: 40px;
  padding: var(--space-2) 0;
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-secondary);
  font-size: 14px;
  font-weight: 620;
  text-decoration: none;
}

.right-rail__links a:last-child {
  border-bottom: 0;
}

.right-rail__links a:hover,
.right-rail__links a:focus-visible {
  color: var(--color-accent);
}
</style>
