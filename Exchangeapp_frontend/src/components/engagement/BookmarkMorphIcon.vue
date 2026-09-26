<template>
  <svg
    class="bookmark-morph-icon"
    :class="{
      'bookmark-morph-icon--bookmarked': bookmarked,
      'bookmark-morph-icon--bookmarking': motion === 'bookmarking',
      'bookmark-morph-icon--unbookmarking': motion === 'unbookmarking',
    }"
    :width="size"
    :height="size"
    viewBox="0 0 36 36"
    aria-hidden="true"
    focusable="false"
    :data-motion="motion"
  >
    <path
      ref="outlinePathRef"
      class="bookmark-morph-icon__outline"
      :d="bookmarkPath"
      fill="none"
      stroke="currentColor"
      stroke-width="2.2"
      stroke-linejoin="round"
      stroke-linecap="round"
    />
    <path
      ref="filledPathRef"
      class="bookmark-morph-icon__filled"
      :d="bookmarkPath"
      fill="currentColor"
      stroke="currentColor"
      stroke-width="1.2"
      stroke-linejoin="round"
    />
    <path
      ref="ribbonPathRef"
      class="bookmark-morph-icon__ribbon"
      :d="ribbonShapes.folded"
      fill="currentColor"
      stroke="none"
    />
  </svg>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { gsap } from 'gsap';
import { MorphSVGPlugin } from 'gsap/MorphSVGPlugin';

gsap.registerPlugin(MorphSVGPlugin);

type BookmarkMorphMotion = 'idle' | 'bookmarking' | 'unbookmarking';

const props = defineProps<{
  size: number;
  bookmarked: boolean;
  motion: BookmarkMorphMotion;
}>();

const bookmarkPath = 'M12 5 H24 C25.7 5 27 6.3 27 8 V31 L18 25.5 L9 31 V8 C9 6.3 10.3 5 12 5 Z';

const ribbonShapes = {
  folded: 'M9 5 H27 V8 H9 Z',
  opened: 'M9 5 H27 V8 C23.8 8.8 20.7 10.7 18 13.5 C15.3 10.7 12.2 8.8 9 8 Z',
  dropping: 'M9 5 H27 V8 C24.2 12 22 16 18 20 C14 16 11.8 12 9 8 Z',
  deepDrop: 'M9 5 H27 V8 C23.5 18 22 27 18 34 C14 27 12.5 18 9 8 Z',
  formedOvershoot: 'M12 5 H24 C25.7 5 27 6.3 27 8 V31 L18 24.5 L9 31 V8 C9 6.3 10.3 5 12 5 Z',
  formed: bookmarkPath,
};

const unbookmarkShapes = {
  compressed: 'M12 5 H24 C25.7 5 27 6.3 27 8 V29.5 L18 27 L9 29.5 V8 C9 6.3 10.3 5 12 5 Z',
  lifted: 'M12 5 H24 C25.7 5 27 6.3 27 8 V18 L18 14.5 L9 18 V8 C9 6.3 10.3 5 12 5 Z',
  retracted: ribbonShapes.folded,
};

const outlinePathRef = ref<SVGPathElement | null>(null);
const filledPathRef = ref<SVGPathElement | null>(null);
const ribbonPathRef = ref<SVGPathElement | null>(null);

let activeTimeline: gsap.core.Timeline | null = null;

const clearActiveTimeline = () => {
  activeTimeline?.kill();
  activeTimeline = null;
};

const prefersReducedMotion = () => (
  typeof window !== 'undefined'
  && typeof window.matchMedia === 'function'
  && window.matchMedia('(prefers-reduced-motion: reduce)').matches
);

const snapToCanonicalState = (nextBookmarked: boolean) => {
  const outlinePath = outlinePathRef.value;
  const filledPath = filledPathRef.value;
  const ribbonPath = ribbonPathRef.value;

  if (!outlinePath || !filledPath || !ribbonPath) {
    return;
  }

  gsap.set([outlinePath, filledPath, ribbonPath], { clearProps: 'transform' });

  filledPath.setAttribute('d', bookmarkPath);
  ribbonPath.setAttribute('d', nextBookmarked ? ribbonShapes.formed : ribbonShapes.folded);

  gsap.set(outlinePath, { opacity: nextBookmarked ? 0 : 1 });
  gsap.set(filledPath, { opacity: nextBookmarked ? 1 : 0 });
  gsap.set(ribbonPath, { opacity: 0 });
};

const createTimeline = () => {
  let timeline: gsap.core.Timeline;
  timeline = gsap.timeline({
    onComplete: () => {
      if (activeTimeline !== timeline) {
        return;
      }

      activeTimeline = null;
      snapToCanonicalState(props.bookmarked);
    },
  });
  activeTimeline = timeline;
  return timeline;
};

const playBookmarkIn = () => {
  const outlinePath = outlinePathRef.value;
  const filledPath = filledPathRef.value;
  const ribbonPath = ribbonPathRef.value;

  if (!outlinePath || !filledPath || !ribbonPath) {
    return;
  }

  filledPath.setAttribute('d', bookmarkPath);
  ribbonPath.setAttribute('d', ribbonShapes.folded);

  const timeline = createTimeline();
  timeline
    .set(outlinePath, { opacity: 1 })
    .set(filledPath, { opacity: 0 })
    .set(ribbonPath, { opacity: 1 })
    .to(ribbonPath, { morphSVG: ribbonShapes.opened, duration: 0.07, ease: 'power2.out' }, 0)
    .to(ribbonPath, { morphSVG: ribbonShapes.dropping, duration: 0.08, ease: 'power2.inOut' }, 0.07)
    .to(ribbonPath, { morphSVG: ribbonShapes.deepDrop, duration: 0.09, ease: 'power2.inOut' }, 0.15)
    .to(ribbonPath, { morphSVG: ribbonShapes.formedOvershoot, duration: 0.09, ease: 'power2.out' }, 0.24)
    .to(ribbonPath, { morphSVG: ribbonShapes.formed, duration: 0.21, ease: 'elastic.out(1, 0.55)' }, 0.33)
    .to(outlinePath, { opacity: 0, duration: 0.18, ease: 'power1.inOut' }, 0.12)
    .to(filledPath, { opacity: 1, duration: 0.19, ease: 'power1.out' }, 0.35)
    .to(ribbonPath, { opacity: 0, duration: 0.14, ease: 'power1.out' }, 0.4);
};

const playBookmarkOut = () => {
  const outlinePath = outlinePathRef.value;
  const filledPath = filledPathRef.value;
  const ribbonPath = ribbonPathRef.value;

  if (!outlinePath || !filledPath || !ribbonPath) {
    return;
  }

  filledPath.setAttribute('d', bookmarkPath);
  ribbonPath.setAttribute('d', ribbonShapes.folded);

  const timeline = createTimeline();
  timeline
    .set(outlinePath, { opacity: 0 })
    .set(filledPath, { opacity: 1 })
    .set(ribbonPath, { opacity: 0 })
    .to(filledPath, { morphSVG: unbookmarkShapes.compressed, duration: 0.08, ease: 'power2.in' }, 0)
    .to(filledPath, { morphSVG: unbookmarkShapes.lifted, duration: 0.09, ease: 'power2.in' }, 0.08)
    .to(filledPath, { morphSVG: unbookmarkShapes.retracted, duration: 0.09, ease: 'power2.inOut' }, 0.17)
    .to(outlinePath, { opacity: 1, duration: 0.12, ease: 'power1.out' }, 0.18)
    .to(filledPath, { opacity: 0, duration: 0.04, ease: 'power1.out' }, 0.26);
};

onMounted(() => {
  snapToCanonicalState(props.bookmarked);
});

watch(
  () => [props.motion, props.bookmarked] as const,
  ([motion, bookmarked], [previousMotion]) => {
    if (motion === previousMotion && motion !== 'idle') {
      return;
    }

    clearActiveTimeline();

    if (motion === 'idle' || prefersReducedMotion()) {
      snapToCanonicalState(bookmarked);
      return;
    }

    if (motion === 'bookmarking') {
      playBookmarkIn();
      return;
    }

    playBookmarkOut();
  },
  { flush: 'post' },
);

onBeforeUnmount(clearActiveTimeline);
</script>

<style scoped>
.bookmark-morph-icon {
  display: block;
  flex: 0 0 auto;
  overflow: visible;
  pointer-events: none;
}

.bookmark-morph-icon__outline {
  fill: none;
  stroke: currentColor;
  stroke-width: 2.2;
  stroke-linejoin: round;
  stroke-linecap: round;
}

.bookmark-morph-icon__filled {
  fill: currentColor;
  stroke: currentColor;
  stroke-width: 1.2;
  stroke-linejoin: round;
}

.bookmark-morph-icon__ribbon {
  fill: currentColor;
}

.bookmark-morph-icon--bookmarked .bookmark-morph-icon__outline {
  opacity: 0;
}

.bookmark-morph-icon--bookmarked .bookmark-morph-icon__filled {
  opacity: 1;
}

.bookmark-morph-icon--bookmarking .bookmark-morph-icon__outline {
  opacity: 1;
}

.bookmark-morph-icon--bookmarking .bookmark-morph-icon__filled {
  opacity: 0;
  fill: var(--color-accent);
  stroke: var(--color-accent);
}

.bookmark-morph-icon--bookmarking .bookmark-morph-icon__ribbon {
  opacity: 1;
  fill: var(--color-accent);
}

.bookmark-morph-icon--unbookmarking .bookmark-morph-icon__outline {
  opacity: 0;
}

.bookmark-morph-icon--unbookmarking .bookmark-morph-icon__filled {
  opacity: 1;
  fill: var(--color-accent);
  stroke: var(--color-accent);
}
</style>
