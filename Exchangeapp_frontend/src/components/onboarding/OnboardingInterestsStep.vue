<template>
  <section class="onboarding-interests" :aria-busy="loadingTopics">
    <div v-if="loadingTopics" class="onboarding-interests__status" role="status">Loading topics…</div>
    <div v-else-if="loadFailed" class="onboarding-interests__status">
      <p>Topics are unavailable right now.</p>
      <button class="onboarding-interests__retry" type="button" @click="loadTopics">Try again</button>
    </div>
    <template v-else>
      <div class="onboarding-interests__grid" role="group" aria-label="Choose topics you are interested in">
        <button
          v-for="topic in topics"
          :key="topic.slug"
          class="onboarding-interests__topic"
          :class="{ 'onboarding-interests__topic--selected': selected.has(topic.slug) }"
          type="button"
          :aria-pressed="selected.has(topic.slug)"
          :disabled="onboarding.saving"
          @click="toggleTopic(topic.slug)"
        >
          <span class="onboarding-interests__topic-top">
            <strong>{{ topic.label }}</strong>
            <span class="onboarding-interests__check" aria-hidden="true">{{ selected.has(topic.slug) ? '✓' : '+' }}</span>
          </span>
          <span class="onboarding-interests__description">{{ topic.description }}</span>
        </button>
      </div>
      <p class="onboarding-interests__count" aria-live="polite">{{ selected.size }} selected</p>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { getTopics, type TopicSummary } from '../../services/topicService';
import { useOnboardingStore } from '../../store/onboarding';
import type { OnboardingRequestContext } from '../../store/onboarding';

const onboarding = useOnboardingStore();
const topics = ref<TopicSummary[]>([]);
const selected = ref(new Set<string>());
const loadingTopics = ref(true);
const loadFailed = ref(false);
let active = true;

const loadTopics = async () => {
  const context = onboarding.captureContext();
  if (!context) return;
  loadingTopics.value = true;
  loadFailed.value = false;
  onboarding.clearError();
  try {
    const response = await getTopics();
    if (!active || !onboarding.matchesContext(context)) return;
    if (!Array.isArray(response.items)) throw new Error('Invalid topics response');
    const available = response.items.filter(topic => (
      topic
      && typeof topic.slug === 'string'
      && topic.slug.length > 0
      && typeof topic.label === 'string'
      && typeof topic.description === 'string'
    ));
    topics.value = available;
    const allowed = new Set(available.map(topic => topic.slug));
    selected.value = new Set(
      (onboarding.status?.interest_slugs ?? []).filter(slug => allowed.has(slug)),
    );
  } catch {
    if (!active || !onboarding.matchesContext(context)) return;
    loadFailed.value = true;
    onboarding.setError('Topics could not be loaded. Retry or skip this step.', true);
  } finally {
    if (active && onboarding.matchesContext(context)) loadingTopics.value = false;
  }
};

const toggleTopic = (slug: string) => {
  if (onboarding.saving) return;
  const next = new Set(selected.value);
  if (next.has(slug)) next.delete(slug);
  else if (next.size < topics.value.length) next.add(slug);
  selected.value = next;
  onboarding.clearError();
};

const saveProgress = async (step: 'follow_users', context: OnboardingRequestContext) => {
  await onboarding.saveProgress(step, context);
};

const next = async () => {
  if (loadingTopics.value || loadFailed.value) return;
  const context = onboarding.beginOperation();
  if (!context) return;
  try {
    const saved = await onboarding.saveInterests([...selected.value], context);
    if (!saved || !onboarding.matchesContext(context)) return;
    selected.value = new Set(saved.interest_slugs);
    await saveProgress('follow_users', context);
  } finally {
    onboarding.finishOperation(context);
  }
};

const skip = async () => {
  selected.value = new Set(onboarding.status?.interest_slugs ?? []);
  const context = onboarding.beginOperation();
  if (!context) return;
  try {
    await saveProgress('follow_users', context);
  } finally {
    onboarding.finishOperation(context);
  }
};

defineExpose({ next, skip, canAdvance: computed(() => !loadingTopics.value && !loadFailed.value) });
onMounted(() => { void loadTopics(); });
onBeforeUnmount(() => { active = false; selected.value.clear(); topics.value = []; });
</script>

<style scoped>
.onboarding-interests { display: grid; align-content: start; gap: var(--space-3); padding: var(--space-4); }
.onboarding-interests__grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--space-3); }
.onboarding-interests__topic { display: grid; align-content: start; gap: var(--space-2); min-height: 112px; border: 1px solid var(--color-border-strong); border-radius: var(--radius-md); padding: var(--space-4); background: var(--color-surface); color: var(--color-text); text-align: left; cursor: pointer; transition: border-color var(--transition-fast), background var(--transition-fast), transform var(--transition-fast); }
.onboarding-interests__topic:hover { border-color: var(--color-accent); transform: translateY(-1px); }
.onboarding-interests__topic--selected { border-color: var(--color-accent); background: color-mix(in srgb, var(--color-accent) 9%, var(--color-surface)); box-shadow: inset 0 0 0 1px var(--color-accent); }
.onboarding-interests__topic-top { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); }
.onboarding-interests__topic-top strong { font-size: 15px; }
.onboarding-interests__check { display: grid; width: 22px; height: 22px; flex: 0 0 auto; place-items: center; border: 1px solid var(--color-border-strong); border-radius: 50%; color: var(--color-text-secondary); font-size: 15px; }
.onboarding-interests__topic--selected .onboarding-interests__check { border-color: var(--color-accent); background: var(--color-accent); color: white; }
.onboarding-interests__description { color: var(--color-text-secondary); font-size: 12px; line-height: 1.4; }
.onboarding-interests__count { margin: 0; color: var(--color-text-tertiary); font-size: 13px; text-align: right; }
.onboarding-interests__status { display: grid; min-height: 190px; align-content: center; justify-items: center; gap: var(--space-3); color: var(--color-text-secondary); text-align: center; }
.onboarding-interests__status p { margin: 0; }
.onboarding-interests__retry { min-height: 44px; border: 0; border-radius: var(--radius-pill); padding-inline: var(--space-5); background: var(--color-surface-subtle); color: var(--color-text); font: inherit; font-weight: 700; cursor: pointer; }
.onboarding-interests button:focus-visible { outline: 3px solid color-mix(in srgb, var(--color-accent) 45%, transparent); outline-offset: 3px; }
@media (max-width: 620px) { .onboarding-interests__grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-2); } .onboarding-interests__topic { min-height: 105px; padding: var(--space-3); } }
</style>
