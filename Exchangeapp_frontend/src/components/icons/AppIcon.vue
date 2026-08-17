<template>
  <svg
    class="app-icon"
    :width="props.size"
    :height="props.size"
    viewBox="0 0 24 24"
    aria-hidden="true"
    focusable="false"
  >
    <template v-if="props.name === 'home'">
      <path
        v-if="props.filled"
        d="M3.75 10.5 12 3.75l8.25 6.75v8.25a1.5 1.5 0 0 1-1.5 1.5H5.25a1.5 1.5 0 0 1-1.5-1.5V10.5Z"
        fill="currentColor"
        stroke="none"
      />
      <path
        v-else
        d="M3.75 10.5 12 3.75l8.25 6.75v8.25a1.5 1.5 0 0 1-1.5 1.5H5.25a1.5 1.5 0 0 1-1.5-1.5V10.5Z"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <path
        v-if="!props.filled"
        d="M9.25 20.25v-5.5h5.5v5.5"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>

    <template v-else-if="props.name === 'exchange'">
      <path
        d="M4 8h14m-3-3 3 3-3 3M20 16H6m3 3-3-3 3-3"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>

    <template v-else-if="props.name === 'search'">
      <circle cx="10.75" cy="10.75" r="5.5" fill="none" stroke="currentColor" stroke-width="1.8" />
      <path d="m15 15 4.25 4.25" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" />
    </template>

    <template v-else-if="props.name === 'profile'">
      <path
        v-if="props.filled"
        d="M12 3.5a4.25 4.25 0 1 1 0 8.5 4.25 4.25 0 0 1 0-8.5Zm-8 16.75a8 8 0 0 1 16 0H4Z"
        fill="currentColor"
        stroke="none"
      />
      <template v-else>
        <circle
          cx="12"
          cy="8"
          r="3.75"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
        />
        <path
          d="M4.25 20.25a7.75 7.75 0 0 1 15.5 0"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </template>
    </template>

    <template v-else-if="props.name === 'compose'">
      <path
        d="M13.25 4.25H6.5A1.75 1.75 0 0 0 4.75 6v12A1.75 1.75 0 0 0 6.5 19.75h11A1.75 1.75 0 0 0 19.25 18v-5.25"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <path
        d="m9 15 .75-3.25L16.6 4.9a2.05 2.05 0 0 1 2.9 2.9l-6.85 6.85L9 15Z"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>

    <template v-else-if="props.name === 'logout'">
      <path
        d="M10 4.25H6.5A1.75 1.75 0 0 0 4.75 6v12a1.75 1.75 0 0 0 1.75 1.75H10"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <path
        d="M10.5 12h8.75m-3.25-3.25L19.25 12 16 15.25"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>

    <template v-else-if="props.name === 'reply'">
      <path
        d="M19.75 11.5a7.25 7.25 0 0 1-7.25 7.25H8l-3.75 2v-4.75A7.25 7.25 0 1 1 19.75 11.5Z"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>

    <template v-else-if="props.name === 'heart'">
      <path
        d="M20.5 8.75c0 5.05-8.5 9.95-8.5 9.95s-8.5-4.9-8.5-9.95A4.55 4.55 0 0 1 12 6.55a4.55 4.55 0 0 1 8.5 2.2Z"
        :fill="props.filled ? 'currentColor' : 'none'"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>

    <template v-else-if="props.name === 'more'">
      <circle cx="5" cy="12" r="1.5" fill="currentColor" stroke="none" />
      <circle cx="12" cy="12" r="1.5" fill="currentColor" stroke="none" />
      <circle cx="19" cy="12" r="1.5" fill="currentColor" stroke="none" />
    </template>

    <template v-else-if="props.name === 'link'">
      <path
        d="m9.5 14.5-1.25 1.25a3.18 3.18 0 1 1-4.5-4.5L6.5 8.5a3.18 3.18 0 0 1 4.5 0m3.5 1 1.25-1.25a3.18 3.18 0 1 1 4.5 4.5L17.5 15.5a3.18 3.18 0 0 1-4.5 0m-3.5-.5 5-5"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>

    <template v-else-if="props.name === 'analytics'">
      <path
        d="M5 18v-4"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
      />
      <path
        d="M12 18V9"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
      />
      <path
        d="M19 18V5"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
      />
    </template>

    <template v-else-if="props.name === 'eye-off'">
      <path
        d="M3.75 3.75 20.25 20.25M10.25 10.25a2.5 2.5 0 0 0 3.5 3.5M6.25 6.75C4.75 8 3.75 9.75 3.25 12c1.25 4.25 4.5 6.75 8.75 6.75 1.3 0 2.5-.25 3.55-.75M9 5.45A10.1 10.1 0 0 1 12 5.25c4.25 0 7.5 2.5 8.75 6.75a11.25 11.25 0 0 1-2.1 3.8"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>

    <template v-else-if="props.name === 'trash'">
      <path
        d="M5 7h14M10 11v6M14 11v6M8 7l1-3h6l1 3m-9 0 .8 13h6.4L15 7"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>

    <template v-else-if="props.name === 'arrow-left'">
      <path
        d="m15.5 5.5-6.5 6.5 6.5 6.5M9.5 12h10"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>

    <template v-else-if="props.name === 'close'">
      <path
        d="m6.5 6.5 11 11m0-11-11 11"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>

    <template v-else-if="props.name === 'camera'">
      <path
        d="M5.25 7.5h3l1.25-2h5l1.25 2h3A1.75 1.75 0 0 1 20.5 9.25v8.5a1.75 1.75 0 0 1-1.75 1.75h-14A1.75 1.75 0 0 1 3 17.75v-8.5A1.75 1.75 0 0 1 4.75 7.5Z"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <circle cx="12" cy="13.5" r="3" fill="none" stroke="currentColor" stroke-width="1.8" />
    </template>

    <template v-else-if="props.name === 'image'">
      <rect
        x="3.5"
        y="4"
        width="17"
        height="16"
        rx="2"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <circle cx="8.5" cy="9" r="1.35" fill="none" stroke="currentColor" stroke-width="1.8" />
      <path
        d="m4.75 17 4.5-4.5 3 3 2-2 5 4.5"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>

    <template v-else-if="props.name === 'image-off'">
      <rect
        x="3.5"
        y="4"
        width="17"
        height="16"
        rx="2"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <path
        d="m4.75 17 4.5-4.5 3 3 2-2 5 4.5M4 4l16 16"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>
  </svg>
</template>

<script setup lang="ts">
type AppIconName =
  | 'home'
  | 'exchange'
  | 'search'
  | 'profile'
  | 'compose'
  | 'logout'
  | 'reply'
  | 'heart'
  | 'more'
  | 'link'
  | 'analytics'
  | 'eye-off'
  | 'trash'
  | 'arrow-left'
  | 'close'
  | 'camera'
  | 'image'
  | 'image-off';

const props = withDefaults(defineProps<{
  name: AppIconName;
  size?: number;
  filled?: boolean;
}>(), {
  size: 24,
  filled: false,
});
</script>

<style scoped>
.app-icon {
  display: block;
  flex: 0 0 auto;
}
</style>
