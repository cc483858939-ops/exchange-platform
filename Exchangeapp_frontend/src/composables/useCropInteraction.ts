import { onBeforeUnmount, ref } from 'vue';

type CropInteractionOptions = {
  isDisabled: () => boolean;
  onPan: (dx: number, dy: number) => void;
};

export const useCropInteraction = (options: CropInteractionOptions) => {
  const sourcePreviewURL = ref('');
  const isDragging = ref(false);

  let sessionVersion = 0;
  let activePointerID: number | null = null;
  let activePointerElement: HTMLElement | null = null;
  let pointerCaptureActive = false;
  let usingWindowPointerFallback = false;
  let previousPointerX = 0;
  let previousPointerY = 0;
  let observedElement: HTMLElement | null = null;
  let resizeObserver: ResizeObserver | null = null;
  let onViewportResize: (() => void) | null = null;
  let handlePointerMove!: (event: PointerEvent) => void;
  let handlePointerUp!: (event: PointerEvent) => void;
  let handlePointerCancel!: (event: PointerEvent) => void;
  const handledFallbackEvents = new WeakSet<PointerEvent>();

  const shouldHandlePointerEvent = (event: PointerEvent) => {
    if (!usingWindowPointerFallback || typeof window === 'undefined') return true;
    if (handledFallbackEvents.has(event)) return false;
    handledFallbackEvents.add(event);
    return true;
  };

  const removeWindowPointerFallback = () => {
    if (!usingWindowPointerFallback) return;
    if (typeof window !== 'undefined') {
      window.removeEventListener('pointermove', handlePointerMove);
      window.removeEventListener('pointerup', handlePointerUp);
      window.removeEventListener('pointercancel', handlePointerCancel);
    }
    usingWindowPointerFallback = false;
  };

  const attachWindowPointerFallback = () => {
    if (usingWindowPointerFallback) return;
    if (typeof window === 'undefined') {
      endPointerDrag(false);
      return;
    }
    usingWindowPointerFallback = true;
    window.addEventListener('pointermove', handlePointerMove);
    window.addEventListener('pointerup', handlePointerUp);
    window.addEventListener('pointercancel', handlePointerCancel);
  };

  const endPointerDrag = (releaseCapture = true) => {
    if (activePointerID === null) return;

    if (releaseCapture && pointerCaptureActive) {
      try {
        activePointerElement?.releasePointerCapture?.(activePointerID);
      } catch {
        // Pointer capture may already have been released by the browser.
      }
    }

    removeWindowPointerFallback();
    activePointerID = null;
    activePointerElement = null;
    pointerCaptureActive = false;
    isDragging.value = false;
  };

  const revokeSourcePreviewURL = () => {
    const currentURL = sourcePreviewURL.value;
    if (!currentURL) return;

    try {
      if (typeof URL !== 'undefined' && typeof URL.revokeObjectURL === 'function') {
        URL.revokeObjectURL(currentURL);
      }
    } catch {
      // Resource cleanup must remain safe if the browser rejects a stale URL.
    }
    sourcePreviewURL.value = '';
  };

  const setSourcePreviewFile = (file: File) => {
    revokeSourcePreviewURL();
    sourcePreviewURL.value = URL.createObjectURL(file);
    return sourcePreviewURL.value;
  };

  const beginSession = () => {
    endPointerDrag();
    sessionVersion += 1;
    return sessionVersion;
  };

  const isSessionCurrent = (version: number) => version === sessionVersion;

  const invalidateSession = () => {
    sessionVersion += 1;
    endPointerDrag();
  };

  const resetInteraction = () => {
    invalidateSession();
  };

  const handleCropKeydown = (event: KeyboardEvent) => {
    if (options.isDisabled()) return;

    const step = event.shiftKey ? 32 : 8;
    let dx = 0;
    let dy = 0;

    switch (event.key) {
      case 'ArrowLeft':
        dx = -step;
        break;
      case 'ArrowRight':
        dx = step;
        break;
      case 'ArrowUp':
        dy = -step;
        break;
      case 'ArrowDown':
        dy = step;
        break;
      default:
        return;
    }

    event.preventDefault();
    options.onPan(dx, dy);
  };

  handlePointerMove = (event: PointerEvent) => {
    if (!shouldHandlePointerEvent(event)) return;
    if (activePointerID === null || event.pointerId !== activePointerID) return;
    if (options.isDisabled()) {
      endPointerDrag();
      return;
    }

    const dx = event.clientX - previousPointerX;
    const dy = event.clientY - previousPointerY;
    previousPointerX = event.clientX;
    previousPointerY = event.clientY;
    options.onPan(dx, dy);
  };

  handlePointerUp = (event: PointerEvent) => {
    if (!shouldHandlePointerEvent(event)) return;
    if (activePointerID !== event.pointerId) return;
    endPointerDrag();
  };

  handlePointerCancel = (event: PointerEvent) => {
    if (!shouldHandlePointerEvent(event)) return;
    if (activePointerID !== event.pointerId) return;
    endPointerDrag();
  };

  const handlePointerDown = (event: PointerEvent) => {
    if (activePointerID !== null || options.isDisabled()) return;

    activePointerID = event.pointerId;
    activePointerElement = event.currentTarget as HTMLElement | null;
    previousPointerX = event.clientX;
    previousPointerY = event.clientY;
    isDragging.value = true;

    const setPointerCapture = activePointerElement?.setPointerCapture;
    if (typeof setPointerCapture !== 'function') {
      attachWindowPointerFallback();
      return;
    }

    try {
      setPointerCapture.call(activePointerElement, event.pointerId);
      pointerCaptureActive = true;
    } catch {
      attachWindowPointerFallback();
    }
  };

  const detachViewport = () => {
    if (onViewportResize && typeof window !== 'undefined') {
      window.removeEventListener('resize', onViewportResize);
    }
    resizeObserver?.disconnect();
    resizeObserver = null;
    onViewportResize = null;
    observedElement = null;
    endPointerDrag();
  };

  const attachViewport = (element: HTMLElement, onResize: () => void) => {
    if (observedElement === element && onViewportResize === onResize) return;
    detachViewport();

    observedElement = element;
    onViewportResize = onResize;
    if (typeof window !== 'undefined') {
      window.addEventListener('resize', onResize);
    }
    if (typeof ResizeObserver !== 'undefined') {
      resizeObserver = new ResizeObserver(() => onViewportResize?.());
      resizeObserver.observe(element);
    }
  };

  onBeforeUnmount(() => {
    invalidateSession();
    detachViewport();
    revokeSourcePreviewURL();
  });

  return {
    sourcePreviewURL,
    setSourcePreviewFile,
    revokeSourcePreviewURL,
    beginSession,
    isSessionCurrent,
    invalidateSession,
    handleCropKeydown,
    handlePointerDown,
    handlePointerMove,
    handlePointerUp,
    handlePointerCancel,
    attachViewport,
    detachViewport,
    isDragging,
    resetInteraction,
  };
};
