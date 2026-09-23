<template>
  <dialog
    ref="dialogRef"
    class="cover-crop-dialog"
    aria-labelledby="cover-crop-title"
    @cancel.prevent="handleCancel"
  >
    <section class="cover-crop-dialog__surface">
      <header class="cover-crop-dialog__header">
        <button
          class="cover-crop-dialog__close"
          type="button"
          aria-label="Close"
          @click="handleCancel"
        >
          <span aria-hidden="true">×</span>
        </button>
        <h2 id="cover-crop-title">Edit cover</h2>
        <span class="cover-crop-dialog__header-spacer" aria-hidden="true"></span>
      </header>

      <div class="cover-crop-dialog__body">
        <div
          ref="cropViewportRef"
          class="cover-crop-dialog__viewport"
          :class="{ 'cover-crop-dialog__viewport--dragging': dragging }"
          aria-label="Cover crop preview"
          role="img"
          @pointerdown="handlePointerDown"
          @pointermove="handlePointerMove"
          @pointerup="handlePointerUp"
          @pointercancel="handlePointerUp"
        >
          <img
            v-if="sourcePreviewURL && geometry"
            class="cover-crop-dialog__image"
            :src="sourcePreviewURL"
            :alt="`${file.name} crop preview`"
            :style="imageStyle"
            draggable="false"
            @dragstart.prevent
          />
          <span v-if="loading" class="cover-crop-dialog__status">Loading cover…</span>
        </div>

        <p v-if="errorMessage" class="cover-crop-dialog__error" role="alert">
          {{ errorMessage }}
        </p>

        <div class="cover-crop-dialog__controls">
          <label for="cover-crop-zoom">Zoom photo</label>
          <div class="cover-crop-dialog__zoom-row">
            <span aria-hidden="true">−</span>
            <input
              id="cover-crop-zoom"
              type="range"
              min="0"
              max="1"
              step="0.001"
              :value="zoomRatio"
              :disabled="!geometry || loading || applying"
              aria-label="Zoom photo"
              @input="handleZoomInput"
            />
            <span aria-hidden="true">+</span>
          </div>
          <button
            class="cover-crop-dialog__reset"
            type="button"
            :disabled="!geometry || loading || applying"
            @click="resetCrop"
          >
            Reset
          </button>
        </div>
      </div>

      <footer class="cover-crop-dialog__actions">
        <button
          class="cover-crop-dialog__button cover-crop-dialog__button--secondary"
          type="button"
          @click="handleCancel"
        >
          Cancel
        </button>
        <button
          class="cover-crop-dialog__button cover-crop-dialog__button--primary cover-crop-dialog__apply"
          type="button"
          :disabled="!geometry || loading || applying || sourceInvalid"
          :aria-busy="applying"
          @click="handleApply"
        >
          {{ applying ? 'Preparing…' : 'Apply' }}
        </button>
      </footer>
    </section>
  </dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import {
  centeredCoverCropState,
  clampCoverCropState,
  createCoverCropGeometry,
  createCroppedCover,
  decodeCoverImage,
  isCoverCropError,
  remapCoverCropState,
  zoomCoverCropState,
  type CoverCropGeometry,
  type CoverCropState,
} from '../../utils/coverCrop';

const props = defineProps<{
  file: File;
}>();

const emit = defineEmits<{
  cancel: [];
  apply: [file: File];
}>();

const defaultViewportWidth = 460;
const dialogRef = ref<HTMLDialogElement | null>(null);
const cropViewportRef = ref<HTMLElement | null>(null);
const sourcePreviewURL = ref('');
const viewportWidth = ref(defaultViewportWidth);
const naturalWidth = ref(0);
const naturalHeight = ref(0);
const scale = ref(1);
const offsetX = ref(0);
const offsetY = ref(0);
const loading = ref(true);
const applying = ref(false);
const sourceInvalid = ref(false);
const errorMessage = ref('');
const dragging = ref(false);
let sessionVersion = 0;
let pointerID: number | null = null;
let previousPointerX = 0;
let previousPointerY = 0;
let resizeObserver: ResizeObserver | null = null;

const geometry = computed<CoverCropGeometry | null>(() => createCoverCropGeometry(
  viewportWidth.value,
  naturalWidth.value,
  naturalHeight.value,
));

const cropState = computed<CoverCropState>(() => ({
  scale: scale.value,
  offsetX: offsetX.value,
  offsetY: offsetY.value,
}));

const imageStyle = computed(() => ({
  width: `${naturalWidth.value * scale.value}px`,
  height: `${naturalHeight.value * scale.value}px`,
  transform: `translate3d(${offsetX.value}px, ${offsetY.value}px, 0)`,
}));

const zoomRatio = computed(() => {
  const current = geometry.value;
  if (!current || current.maxScale === current.minScale) return 0;
  return (scale.value - current.minScale) / (current.maxScale - current.minScale);
});

const revokeSourcePreviewURL = () => {
  if (!sourcePreviewURL.value) return;
  if (typeof URL !== 'undefined' && typeof URL.revokeObjectURL === 'function') {
    URL.revokeObjectURL(sourcePreviewURL.value);
  }
  sourcePreviewURL.value = '';
};

const setCropState = (state: CoverCropState) => {
  scale.value = state.scale;
  offsetX.value = state.offsetX;
  offsetY.value = state.offsetY;
};

const resetCrop = () => {
  if (!geometry.value) return;
  setCropState(centeredCoverCropState(geometry.value));
};

const measureCropViewport = () => {
  const measured = cropViewportRef.value?.clientWidth ?? 0;
  const oldGeometry = geometry.value;
  if (measured <= 0 || !oldGeometry || measured === viewportWidth.value) return;

  const nextGeometry = createCoverCropGeometry(measured, naturalWidth.value, naturalHeight.value);
  if (!nextGeometry) return;

  const nextState = remapCoverCropState(cropState.value, oldGeometry, nextGeometry);
  viewportWidth.value = measured;
  setCropState(nextState);
};

const openDialog = () => {
  const dialog = dialogRef.value;
  if (!dialog) return;
  if (typeof dialog.showModal === 'function' && !dialog.open) {
    try {
      dialog.showModal();
      return;
    } catch {
      // A non-modal fallback keeps the crop flow usable in older browsers.
    }
  }
  dialog.setAttribute('open', '');
};

const initialize = async () => {
  const currentVersion = ++sessionVersion;
  pointerID = null;
  dragging.value = false;
  loading.value = true;
  applying.value = false;
  sourceInvalid.value = false;
  errorMessage.value = '';
  naturalWidth.value = 0;
  naturalHeight.value = 0;
  revokeSourcePreviewURL();

  if (typeof URL === 'undefined' || typeof URL.createObjectURL !== 'function') {
    loading.value = false;
    sourceInvalid.value = true;
    errorMessage.value = 'This image could not be opened. Try another cover.';
    return;
  }

  try {
    sourcePreviewURL.value = URL.createObjectURL(props.file);
    const decoded = await decodeCoverImage(props.file);
    if (currentVersion !== sessionVersion) {
      decoded.dispose();
      return;
    }
    naturalWidth.value = decoded.naturalWidth;
    naturalHeight.value = decoded.naturalHeight;
    decoded.dispose();

    if (geometry.value) {
      setCropState(centeredCoverCropState(geometry.value));
    }
    await nextTick();
    if (currentVersion !== sessionVersion) return;
    measureCropViewport();
    loading.value = false;
  } catch (error) {
    if (currentVersion !== sessionVersion) return;
    loading.value = false;
    sourceInvalid.value = true;
    errorMessage.value = isCoverCropError(error, 'SOURCE_TOO_LARGE')
      ? 'This cover is too large. Choose a smaller image.'
      : 'This image could not be opened. Try another cover.';
  }
};

const handleZoomInput = (event: Event) => {
  const current = geometry.value;
  if (!current || loading.value || applying.value) return;
  const ratio = Number((event.target as HTMLInputElement).value);
  if (!Number.isFinite(ratio)) return;
  setCropState(zoomCoverCropState(
    cropState.value,
    current.minScale + ratio * (current.maxScale - current.minScale),
    current,
  ));
};

const handlePointerDown = (event: PointerEvent) => {
  if (pointerID !== null || loading.value || applying.value || !geometry.value) return;
  pointerID = event.pointerId;
  dragging.value = true;
  previousPointerX = event.clientX;
  previousPointerY = event.clientY;
  cropViewportRef.value?.setPointerCapture?.(event.pointerId);
};

const handlePointerMove = (event: PointerEvent) => {
  if (pointerID === null || event.pointerId !== pointerID || !geometry.value || applying.value) return;
  const nextState = clampCoverCropState({
    scale: scale.value,
    offsetX: offsetX.value + event.clientX - previousPointerX,
    offsetY: offsetY.value + event.clientY - previousPointerY,
  }, geometry.value);
  previousPointerX = event.clientX;
  previousPointerY = event.clientY;
  setCropState(nextState);
};

const handlePointerUp = (event: PointerEvent) => {
  if (pointerID !== event.pointerId) return;
  cropViewportRef.value?.releasePointerCapture?.(event.pointerId);
  pointerID = null;
  dragging.value = false;
};

const handleCancel = () => {
  emit('cancel');
};

const handleApply = async () => {
  const currentGeometry = geometry.value;
  if (!currentGeometry || loading.value || applying.value || sourceInvalid.value) return;
  const currentVersion = sessionVersion;
  applying.value = true;
  errorMessage.value = '';
  try {
    const croppedFile = await createCroppedCover({
      source: props.file,
      viewportWidth: currentGeometry.viewportWidth,
      naturalWidth: currentGeometry.naturalWidth,
      naturalHeight: currentGeometry.naturalHeight,
      scale: scale.value,
      offsetX: offsetX.value,
      offsetY: offsetY.value,
    });
    if (currentVersion !== sessionVersion) return;
    emit('apply', croppedFile);
  } catch {
    if (currentVersion === sessionVersion) {
      errorMessage.value = 'Could not prepare this cover. Try another image.';
    }
  } finally {
    if (currentVersion === sessionVersion) applying.value = false;
  }
};

watch(() => props.file, () => {
  void initialize();
});

onMounted(() => {
  openDialog();
  void initialize();
  window.addEventListener('resize', measureCropViewport);
  if (typeof ResizeObserver !== 'undefined' && cropViewportRef.value) {
    resizeObserver = new ResizeObserver(measureCropViewport);
    resizeObserver.observe(cropViewportRef.value);
  }
});

onBeforeUnmount(() => {
  sessionVersion += 1;
  pointerID = null;
  dragging.value = false;
  resizeObserver?.disconnect();
  resizeObserver = null;
  window.removeEventListener('resize', measureCropViewport);
  revokeSourcePreviewURL();
});
</script>

<style scoped>
.cover-crop-dialog {
  width: min(calc(100% - 32px), 520px);
  max-width: calc(100% - 32px);
  max-height: min(90vh, 760px);
  max-height: min(90dvh, 760px);
  box-sizing: border-box;
  margin: auto;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  padding: 0;
  overflow: hidden;
  background: var(--color-surface);
  color: var(--color-text);
}

.cover-crop-dialog::backdrop {
  background: rgba(15, 20, 25, 0.72);
}

.cover-crop-dialog__surface {
  display: grid;
  max-height: inherit;
  grid-template-rows: auto minmax(0, 1fr) auto;
}

.cover-crop-dialog__header,
.cover-crop-dialog__actions {
  display: flex;
  align-items: center;
}

.cover-crop-dialog__header {
  min-height: 60px;
  justify-content: space-between;
  gap: var(--space-3);
  padding: 0 var(--space-4);
  border-bottom: 1px solid var(--color-border);
}

.cover-crop-dialog__header h2 {
  margin: 0;
  font-size: 19px;
  font-weight: 750;
  letter-spacing: -0.02em;
}

.cover-crop-dialog__close,
.cover-crop-dialog__header-spacer {
  display: inline-grid;
  width: 40px;
  height: 40px;
  flex: 0 0 40px;
  place-items: center;
}

.cover-crop-dialog__close {
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
  font: inherit;
  font-size: 28px;
  line-height: 1;
}

.cover-crop-dialog__close:hover,
.cover-crop-dialog__close:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-text);
}

.cover-crop-dialog__body {
  display: grid;
  justify-items: center;
  align-content: center;
  gap: var(--space-4);
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-5) var(--space-4);
}

.cover-crop-dialog__viewport {
  position: relative;
  width: min(460px, calc(100vw - 32px));
  aspect-ratio: 3 / 1;
  flex: 0 0 auto;
  overflow: hidden;
  touch-action: none;
  user-select: none;
  background: #111820;
}

.cover-crop-dialog__image {
  position: absolute;
  top: 0;
  left: 0;
  max-width: none;
  transform-origin: 0 0;
  pointer-events: none;
  user-select: none;
}

.cover-crop-dialog__status {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: #fff;
  font-size: 14px;
  font-weight: 650;
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.55);
}

.cover-crop-dialog__error {
  max-width: 460px;
  margin: 0;
  color: var(--color-danger);
  font-size: 13px;
  line-height: 1.4;
  text-align: center;
}

.cover-crop-dialog__controls {
  display: grid;
  width: min(460px, 100%);
  gap: var(--space-2);
  color: var(--color-text-secondary);
  font-size: 13px;
  font-weight: 650;
}

.cover-crop-dialog__zoom-row {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-3);
  font-size: 20px;
}

.cover-crop-dialog__zoom-row input {
  width: 100%;
  accent-color: var(--color-accent);
}

.cover-crop-dialog__reset {
  justify-self: start;
  min-height: 36px;
  border: 0;
  padding: 0 var(--space-2);
  background: transparent;
  color: var(--color-accent);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
  font-weight: 700;
}

.cover-crop-dialog__reset:disabled {
  cursor: wait;
  opacity: 0.5;
}

.cover-crop-dialog__actions {
  justify-content: flex-end;
  gap: var(--space-2);
  padding: var(--space-4);
  padding-bottom: calc(var(--space-4) + env(safe-area-inset-bottom));
  border-top: 1px solid var(--color-border);
}

.cover-crop-dialog__button {
  min-width: 92px;
  min-height: 42px;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-pill);
  padding: 0 var(--space-4);
  cursor: pointer;
  font: inherit;
  font-weight: 700;
}

.cover-crop-dialog__button--secondary {
  background: var(--color-surface);
  color: var(--color-text);
}

.cover-crop-dialog__button--primary {
  border-color: var(--color-accent);
  background: var(--color-accent);
  color: #fff;
}

.cover-crop-dialog__button:disabled,
.cover-crop-dialog__close:disabled {
  cursor: wait;
  opacity: 0.55;
}

@media (pointer: fine) {
  .cover-crop-dialog__viewport {
    cursor: grab;
  }

  .cover-crop-dialog__viewport--dragging {
    cursor: grabbing;
  }
}

@media (max-width: 520px) {
  .cover-crop-dialog {
    width: 100%;
    max-width: none;
    height: 100vh;
    max-height: 100vh;
    height: 100dvh;
    max-height: 100dvh;
    margin: 0;
    border: 0;
    border-radius: 0;
  }

  .cover-crop-dialog__body {
    padding-inline: var(--space-4);
  }

  .cover-crop-dialog__close,
  .cover-crop-dialog__header-spacer {
    width: 44px;
    height: 44px;
    flex-basis: 44px;
  }

  .cover-crop-dialog__reset {
    min-height: 44px;
  }

  .cover-crop-dialog__viewport {
    width: min(calc(100vw - 32px), 460px);
  }

  .cover-crop-dialog__actions {
    justify-content: space-between;
  }

  .cover-crop-dialog__button {
    min-height: 44px;
  }
}
</style>
