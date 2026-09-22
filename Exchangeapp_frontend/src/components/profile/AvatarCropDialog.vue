<template>
  <dialog
    ref="dialogRef"
    class="avatar-crop-dialog"
    aria-labelledby="avatar-crop-title"
    @cancel.prevent="handleCancel"
  >
    <section class="avatar-crop-dialog__surface">
      <header class="avatar-crop-dialog__header">
        <button
          class="avatar-crop-dialog__close"
          type="button"
          aria-label="Close"
          @click="handleCancel"
        >
          <span aria-hidden="true">×</span>
        </button>
        <h2 id="avatar-crop-title">Edit photo</h2>
        <span class="avatar-crop-dialog__header-spacer" aria-hidden="true"></span>
      </header>

      <div class="avatar-crop-dialog__body">
        <div
          ref="cropViewportRef"
          class="avatar-crop-dialog__viewport"
          aria-label="Avatar crop preview"
          role="img"
          @pointerdown="handlePointerDown"
          @pointermove="handlePointerMove"
          @pointerup="handlePointerUp"
          @pointercancel="handlePointerUp"
        >
          <img
            v-if="sourcePreviewURL && geometry"
            class="avatar-crop-dialog__image"
            :src="sourcePreviewURL"
            :alt="`${file.name} crop preview`"
            :style="imageStyle"
            draggable="false"
            @dragstart.prevent
          />
          <span class="avatar-crop-dialog__circle" aria-hidden="true"></span>
          <span v-if="loading" class="avatar-crop-dialog__status">Loading photo…</span>
        </div>

        <p v-if="errorMessage" class="avatar-crop-dialog__error" role="alert">
          {{ errorMessage }}
        </p>

        <div class="avatar-crop-dialog__controls">
          <label for="avatar-crop-zoom">Zoom photo</label>
          <div class="avatar-crop-dialog__zoom-row">
            <span aria-hidden="true">−</span>
            <input
              id="avatar-crop-zoom"
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
            class="avatar-crop-dialog__reset"
            type="button"
            :disabled="!geometry || loading || applying"
            @click="resetCrop"
          >
            Reset
          </button>
        </div>
      </div>

      <footer class="avatar-crop-dialog__actions">
        <button
          class="avatar-crop-dialog__button avatar-crop-dialog__button--secondary"
          type="button"
          :disabled="applying"
          @click="handleCancel"
        >
          Cancel
        </button>
        <button
          class="avatar-crop-dialog__button avatar-crop-dialog__button--primary"
          type="button"
          :disabled="!geometry || loading || applying || Boolean(errorMessage)"
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
  clampAvatarCropState,
  createAvatarCropGeometry,
  createCroppedAvatar,
  centeredAvatarCropState,
  decodeAvatarImage,
  zoomAvatarCropState,
  type AvatarCropGeometry,
  type AvatarCropState,
} from '../../utils/avatarCrop';

const props = defineProps<{
  file: File;
}>();

const emit = defineEmits<{
  cancel: [];
  apply: [file: File];
}>();

const dialogRef = ref<HTMLDialogElement | null>(null);
const cropViewportRef = ref<HTMLElement | null>(null);
const sourcePreviewURL = ref('');
const cropSize = ref(360);
const naturalWidth = ref(0);
const naturalHeight = ref(0);
const scale = ref(1);
const offsetX = ref(0);
const offsetY = ref(0);
const loading = ref(true);
const applying = ref(false);
const errorMessage = ref('');
let sessionVersion = 0;
let pointerID: number | null = null;
let previousPointerX = 0;
let previousPointerY = 0;
let resizeObserver: ResizeObserver | null = null;

const geometry = computed<AvatarCropGeometry | null>(() => createAvatarCropGeometry(
  cropSize.value,
  naturalWidth.value,
  naturalHeight.value,
));

const cropState = computed<AvatarCropState>(() => ({
  scale: scale.value,
  offsetX: offsetX.value,
  offsetY: offsetY.value,
}));

const displayWidth = computed(() => naturalWidth.value * scale.value);
const displayHeight = computed(() => naturalHeight.value * scale.value);
const imageStyle = computed(() => ({
  width: `${displayWidth.value}px`,
  height: `${displayHeight.value}px`,
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

const setCropState = (state: AvatarCropState) => {
  scale.value = state.scale;
  offsetX.value = state.offsetX;
  offsetY.value = state.offsetY;
};

const resetCrop = () => {
  if (!geometry.value) return;
  setCropState(centeredAvatarCropState(geometry.value));
};

const measureCropViewport = () => {
  const measured = cropViewportRef.value?.clientWidth ?? 0;
  if (measured <= 0 || !geometry.value || measured === cropSize.value) return;
  const previousState = cropState.value;
  cropSize.value = measured;
  const nextGeometry = createAvatarCropGeometry(measured, naturalWidth.value, naturalHeight.value);
  if (nextGeometry) {
    setCropState(clampAvatarCropState(previousState, nextGeometry));
  }
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
  loading.value = true;
  applying.value = false;
  errorMessage.value = '';
  naturalWidth.value = 0;
  naturalHeight.value = 0;
  revokeSourcePreviewURL();

  if (typeof URL === 'undefined' || typeof URL.createObjectURL !== 'function') {
    loading.value = false;
    errorMessage.value = 'This image could not be opened. Try another photo.';
    return;
  }

  sourcePreviewURL.value = URL.createObjectURL(props.file);
  try {
    const decoded = await decodeAvatarImage(props.file);
    if (currentVersion !== sessionVersion) {
      decoded.dispose();
      return;
    }
    naturalWidth.value = decoded.naturalWidth;
    naturalHeight.value = decoded.naturalHeight;
    decoded.dispose();
    await nextTick();
    if (currentVersion !== sessionVersion) return;
    measureCropViewport();
    if (geometry.value) resetCrop();
    loading.value = false;
  } catch {
    if (currentVersion !== sessionVersion) return;
    loading.value = false;
    errorMessage.value = 'This image could not be opened. Try another photo.';
  }
};

const handleZoomInput = (event: Event) => {
  const current = geometry.value;
  if (!current) return;
  const ratio = Number((event.target as HTMLInputElement).value);
  if (!Number.isFinite(ratio)) return;
  setCropState(zoomAvatarCropState(
    cropState.value,
    current.minScale + ratio * (current.maxScale - current.minScale),
    current,
  ));
};

const handlePointerDown = (event: PointerEvent) => {
  if (loading.value || applying.value || !geometry.value) return;
  pointerID = event.pointerId;
  previousPointerX = event.clientX;
  previousPointerY = event.clientY;
  cropViewportRef.value?.setPointerCapture?.(event.pointerId);
};

const handlePointerMove = (event: PointerEvent) => {
  if (pointerID === null || event.pointerId !== pointerID || !geometry.value) return;
  const nextState = clampAvatarCropState({
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
};

const handleCancel = () => {
  if (applying.value) return;
  emit('cancel');
};

const handleApply = async () => {
  const currentGeometry = geometry.value;
  if (!currentGeometry || loading.value || applying.value || errorMessage.value) return;
  const currentVersion = sessionVersion;
  applying.value = true;
  errorMessage.value = '';
  try {
    const croppedFile = await createCroppedAvatar({
      source: props.file,
      cropSize: currentGeometry.cropSize,
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
      errorMessage.value = 'Could not prepare this photo. Try another image.';
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
  resizeObserver?.disconnect();
  resizeObserver = null;
  window.removeEventListener('resize', measureCropViewport);
  revokeSourcePreviewURL();
});
</script>

<style scoped>
.avatar-crop-dialog {
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

.avatar-crop-dialog::backdrop {
  background: rgba(15, 20, 25, 0.72);
}

.avatar-crop-dialog__surface {
  display: grid;
  max-height: inherit;
  grid-template-rows: auto minmax(0, 1fr) auto;
}

.avatar-crop-dialog__header,
.avatar-crop-dialog__actions {
  display: flex;
  align-items: center;
}

.avatar-crop-dialog__header {
  min-height: 60px;
  justify-content: space-between;
  gap: var(--space-3);
  padding: 0 var(--space-4);
  border-bottom: 1px solid var(--color-border);
}

.avatar-crop-dialog__header h2 {
  margin: 0;
  font-size: 19px;
  font-weight: 750;
  letter-spacing: -0.02em;
}

.avatar-crop-dialog__close,
.avatar-crop-dialog__header-spacer {
  display: inline-grid;
  width: 40px;
  height: 40px;
  flex: 0 0 40px;
  place-items: center;
}

.avatar-crop-dialog__close {
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
  font: inherit;
  font-size: 28px;
  line-height: 1;
}

.avatar-crop-dialog__close:hover,
.avatar-crop-dialog__close:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-text);
}

.avatar-crop-dialog__body {
  display: grid;
  justify-items: center;
  gap: var(--space-4);
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-5) var(--space-4);
}

.avatar-crop-dialog__viewport {
  position: relative;
  width: min(360px, calc(100vw - 32px));
  aspect-ratio: 1;
  flex: 0 0 auto;
  overflow: hidden;
  touch-action: none;
  user-select: none;
  background: #111820;
}

.avatar-crop-dialog__image {
  position: absolute;
  top: 0;
  left: 0;
  max-width: none;
  transform-origin: 0 0;
  pointer-events: none;
  user-select: none;
}

.avatar-crop-dialog__circle {
  position: absolute;
  inset: 10%;
  border: 2px solid rgba(255, 255, 255, 0.96);
  border-radius: 50%;
  box-shadow: 0 0 0 9999px rgba(0, 0, 0, 0.5);
  pointer-events: none;
}

.avatar-crop-dialog__status {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: #fff;
  font-size: 14px;
  font-weight: 650;
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.55);
}

.avatar-crop-dialog__error {
  max-width: 360px;
  margin: 0;
  color: var(--color-danger);
  font-size: 13px;
  line-height: 1.4;
  text-align: center;
}

.avatar-crop-dialog__controls {
  display: grid;
  width: min(360px, 100%);
  gap: var(--space-2);
  color: var(--color-text-secondary);
  font-size: 13px;
  font-weight: 650;
}

.avatar-crop-dialog__zoom-row {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-3);
  font-size: 20px;
}

.avatar-crop-dialog__zoom-row input {
  width: 100%;
  accent-color: var(--color-accent);
}

.avatar-crop-dialog__reset {
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

.avatar-crop-dialog__reset:disabled {
  cursor: wait;
  opacity: 0.5;
}

.avatar-crop-dialog__actions {
  justify-content: flex-end;
  gap: var(--space-2);
  padding: var(--space-4);
  padding-bottom: calc(var(--space-4) + env(safe-area-inset-bottom));
  border-top: 1px solid var(--color-border);
}

.avatar-crop-dialog__button {
  min-width: 92px;
  min-height: 42px;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-pill);
  padding: 0 var(--space-4);
  cursor: pointer;
  font: inherit;
  font-weight: 700;
}

.avatar-crop-dialog__button--secondary {
  background: var(--color-surface);
  color: var(--color-text);
}

.avatar-crop-dialog__button--primary {
  border-color: var(--color-accent);
  background: var(--color-accent);
  color: #fff;
}

.avatar-crop-dialog__button:disabled,
.avatar-crop-dialog__close:disabled {
  cursor: wait;
  opacity: 0.55;
}

@media (max-width: 520px) {
  .avatar-crop-dialog {
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

  .avatar-crop-dialog__body {
    align-content: center;
    padding-inline: var(--space-4);
  }

  .avatar-crop-dialog__viewport {
    width: min(calc(100vw - 32px), 360px);
  }

  .avatar-crop-dialog__actions {
    justify-content: space-between;
  }

  .avatar-crop-dialog__button {
    min-height: 44px;
  }
}
</style>
