<template>
  <section class="not-found" aria-labelledby="not-found-title">
    <div class="not-found__content">
      <p class="not-found__code" aria-hidden="true">404</p>

      <h1 id="not-found-title">Page not found</h1>

      <p class="not-found__description">
        The page you’re looking for doesn’t exist or may have moved.
      </p>

      <RouterLink class="not-found__action" :to="{ name: 'Home' }">
        Go to Home
      </RouterLink>
    </div>
  </section>
</template>

<style scoped>
.not-found {
  display: grid;
  min-height: 100vh;
  place-items: center;
  padding: clamp(var(--space-8), 10vw, 96px) var(--space-5);
  background: var(--color-surface);
  color: var(--color-text);
  text-align: center;
}

.not-found__content {
  width: min(100%, 520px);
}

.not-found__code {
  margin: 0 0 var(--space-4);
  color: var(--color-accent);
  font-size: clamp(64px, 12vw, 104px);
  font-weight: 800;
  letter-spacing: -0.08em;
  line-height: 0.9;
}

.not-found h1 {
  margin: 0;
  font-size: clamp(26px, 4vw, 36px);
  letter-spacing: -0.03em;
  line-height: 1.15;
}

.not-found__description {
  max-width: 420px;
  margin: var(--space-3) auto 0;
  color: var(--color-text-secondary);
  line-height: 1.6;
}

.not-found__action {
  display: inline-flex;
  min-height: 42px;
  align-items: center;
  justify-content: center;
  margin-top: var(--space-6);
  border: 1px solid var(--color-accent);
  border-radius: var(--radius-pill);
  padding: 0 var(--space-5);
  background: var(--color-accent);
  color: var(--color-surface);
  font-size: 14px;
  font-weight: 750;
  text-decoration: none;
  transition: background-color var(--transition-fast), border-color var(--transition-fast);
}

.not-found__action:hover,
.not-found__action:focus-visible {
  border-color: var(--color-accent-hover);
  background: var(--color-accent-hover);
}

@media (max-width: 799px) {
  .not-found {
    min-height: calc(100dvh - var(--mobile-safe-top) - var(--mobile-bottom-nav-height) - var(--mobile-safe-bottom));
    padding-inline: var(--space-4);
  }
}
</style>
