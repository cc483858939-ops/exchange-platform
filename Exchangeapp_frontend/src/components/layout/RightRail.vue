<template>
  <aside class="right-rail" aria-label="Explore and market quick access">
    <section class="right-rail__section" aria-labelledby="right-rail-topics-heading">
      <p class="right-rail__eyebrow">EXPLORE</p>
      <h2 id="right-rail-topics-heading">Topics</h2>
      <p v-if="topicsUnavailable" class="right-rail__status" role="status">Topics unavailable</p>
      <nav v-else-if="topics.length > 0" class="right-rail__topics" aria-label="Topics">
        <RouterLink
          v-for="topic in topics"
          :key="topic.slug"
          class="right-rail__topic"
          :to="{ name: 'Topic', params: { slug: topic.slug } }"
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
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { getTopics, type TopicSummary } from '../../services/topicService';

const topics = ref<TopicSummary[]>([]);
const topicsUnavailable = ref(false);
let active = true;

onMounted(async () => {
  try {
    const response = await getTopics();
    if (active) topics.value = response.items ?? [];
  } catch {
    if (active) topicsUnavailable.value = true;
  }
});

onBeforeUnmount(() => { active = false; });
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
