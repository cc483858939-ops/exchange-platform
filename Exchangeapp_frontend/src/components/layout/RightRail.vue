<template>
  <section class="right-rail" aria-label="Market quick access">
    <p class="right-rail__eyebrow">MARKET</p>
    <h2>Tools</h2>
    <nav class="right-rail__links" aria-label="Market quick access links">
      <router-link :to="{ name: 'CurrencyExchange' }">Exchange</router-link>
    </nav>
  </section>
</template>

<style scoped>
.right-rail {
  margin-top: var(--space-5);
  padding: var(--space-5);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface);
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
