// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import { useCropInteraction } from './useCropInteraction';

type CropInteraction = ReturnType<typeof useCropInteraction>;

const originalCreateObjectURL = Object.getOwnPropertyDescriptor(URL, 'createObjectURL');
const originalRevokeObjectURL = Object.getOwnPropertyDescriptor(URL, 'revokeObjectURL');
let createObjectURL = vi.fn((file: File) => `blob:${file.name}`);
let revokeObjectURL = vi.fn();

class MockResizeObserver {
  static latest: MockResizeObserver | null = null;

  observedElements: Element[] = [];
  disconnectCount = 0;

  constructor(private readonly callback: ResizeObserverCallback) {
    MockResizeObserver.latest = this;
  }

  observe(element: Element) {
    this.observedElements.push(element);
  }

  unobserve() {}

  disconnect() {
    this.disconnectCount += 1;
  }

  takeRecords(): ResizeObserverEntry[] {
    return [];
  }

  trigger() {
    this.callback([], this as unknown as ResizeObserver);
  }
}

const mountInteraction = (options: Parameters<typeof useCropInteraction>[0]) => {
  let interaction!: CropInteraction;
  const wrapper = mount(defineComponent({
    setup() {
      interaction = useCropInteraction(options);
      return () => h('div');
    },
  }));

  return { wrapper, interaction };
};

const bindPointerEvents = (element: HTMLElement, interaction: CropInteraction) => {
  element.addEventListener('pointerdown', event => interaction.handlePointerDown(event as PointerEvent));
  element.addEventListener('pointermove', event => interaction.handlePointerMove(event as PointerEvent));
  element.addEventListener('pointerup', event => interaction.handlePointerUp(event as PointerEvent));
  element.addEventListener('pointercancel', event => interaction.handlePointerCancel(event as PointerEvent));
  element.addEventListener('lostpointercapture', event => interaction.handlePointerCancel(event as PointerEvent));
};

const dispatchPointer = (
  element: EventTarget,
  type: string,
  pointerId: number,
  clientX: number,
  clientY: number,
) => {
  const event = new Event(type, { bubbles: true });
  Object.defineProperties(event, {
    pointerId: { value: pointerId },
    clientX: { value: clientX },
    clientY: { value: clientY },
  });
  element.dispatchEvent(event);
};

afterAll(() => {
  if (originalCreateObjectURL) {
    Object.defineProperty(URL, 'createObjectURL', originalCreateObjectURL);
  } else {
    Reflect.deleteProperty(URL, 'createObjectURL');
  }
  if (originalRevokeObjectURL) {
    Object.defineProperty(URL, 'revokeObjectURL', originalRevokeObjectURL);
  } else {
    Reflect.deleteProperty(URL, 'revokeObjectURL');
  }
});

beforeEach(() => {
  createObjectURL = vi.fn((file: File) => `blob:${file.name}`);
  revokeObjectURL = vi.fn();
  Object.defineProperty(URL, 'createObjectURL', {
    configurable: true,
    value: createObjectURL,
  });
  Object.defineProperty(URL, 'revokeObjectURL', {
    configurable: true,
    value: revokeObjectURL,
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  MockResizeObserver.latest = null;
});

describe('useCropInteraction', () => {
  it('invalidates an older initialization session when a newer one begins', async () => {
    const { wrapper, interaction } = mountInteraction({
      isDisabled: () => false,
      onPan: () => undefined,
    });
    const oldSession = interaction.beginSession();
    let resolveOldResult!: (value: string) => void;
    const oldCompletion = new Promise<string>(resolve => { resolveOldResult = resolve; })
      .then(value => interaction.isSessionCurrent(oldSession) ? value : null);

    const currentSession = interaction.beginSession();
    resolveOldResult('A');

    expect(await oldCompletion).toBeNull();
    expect(interaction.isSessionCurrent(oldSession)).toBe(false);
    expect(interaction.isSessionCurrent(currentSession)).toBe(true);
    wrapper.unmount();
  });

  it('replaces and revokes source URLs once, including repeated cleanup', () => {
    const { wrapper, interaction } = mountInteraction({
      isDisabled: () => false,
      onPan: () => undefined,
    });
    const first = new File(['first'], 'first.webp', { type: 'image/webp' });
    const second = new File(['second'], 'second.webp', { type: 'image/webp' });

    interaction.setSourcePreviewFile(first);
    interaction.setSourcePreviewFile(second);
    interaction.revokeSourcePreviewURL();
    interaction.revokeSourcePreviewURL();

    expect(createObjectURL.mock.calls.map(([file]) => file.name)).toEqual(['first.webp', 'second.webp']);
    expect(revokeObjectURL.mock.calls).toEqual([['blob:first.webp'], ['blob:second.webp']]);
    expect(interaction.sourcePreviewURL.value).toBe('');
    wrapper.unmount();
    expect(revokeObjectURL).toHaveBeenCalledTimes(2);
  });

  it('tracks one pointer, ignores other pointers, and clears drag on cancel', () => {
    const onPan = vi.fn((_dx: number, _dy: number) => undefined);
    let disabled = false;
    const { wrapper, interaction } = mountInteraction({
      isDisabled: () => disabled,
      onPan,
    });
    const viewport = wrapper.element as HTMLElement;
    const setPointerCapture = vi.fn();
    const releasePointerCapture = vi.fn();
    Object.defineProperty(viewport, 'setPointerCapture', { configurable: true, value: setPointerCapture });
    Object.defineProperty(viewport, 'releasePointerCapture', { configurable: true, value: releasePointerCapture });
    bindPointerEvents(viewport, interaction);

    dispatchPointer(viewport, 'pointerdown', 1, 100, 100);
    dispatchPointer(viewport, 'pointerdown', 2, 400, 400);
    dispatchPointer(viewport, 'pointermove', 2, 410, 410);
    dispatchPointer(viewport, 'pointerup', 2, 410, 410);
    expect(interaction.isDragging.value).toBe(true);
    expect(onPan).not.toHaveBeenCalled();

    dispatchPointer(viewport, 'pointermove', 1, 112, 97);
    expect(onPan).toHaveBeenCalledExactlyOnceWith(12, -3);
    dispatchPointer(viewport, 'pointercancel', 1, 112, 97);
    expect(interaction.isDragging.value).toBe(false);
    expect(releasePointerCapture).toHaveBeenCalledWith(1);

    disabled = true;
    dispatchPointer(viewport, 'pointerdown', 3, 0, 0);
    expect(interaction.isDragging.value).toBe(false);
    wrapper.unmount();
  });

  it('continues drag and clears state through window events when pointer capture fails', () => {
    const onPan = vi.fn((_dx: number, _dy: number) => undefined);
    const { wrapper, interaction } = mountInteraction({
      isDisabled: () => false,
      onPan,
    });
    const viewport = wrapper.element as HTMLElement;
    Object.defineProperty(viewport, 'setPointerCapture', {
      configurable: true,
      value: () => { throw new Error('capture failed'); },
    });
    bindPointerEvents(viewport, interaction);

    dispatchPointer(viewport, 'pointerdown', 7, 20, 20);
    dispatchPointer(window, 'pointermove', 7, 40, 35);
    expect(onPan).toHaveBeenCalledExactlyOnceWith(20, 15);
    expect(interaction.isDragging.value).toBe(true);
    dispatchPointer(window, 'pointerup', 7, 40, 35);

    expect(interaction.isDragging.value).toBe(false);
    wrapper.unmount();
  });

  it('normalizes arrow keys and Shift steps while leaving other or disabled keys alone', () => {
    const onPan = vi.fn((_dx: number, _dy: number) => undefined);
    let disabled = false;
    const { wrapper, interaction } = mountInteraction({
      isDisabled: () => disabled,
      onPan,
    });
    const viewport = wrapper.element as HTMLElement;
    viewport.addEventListener('keydown', event => interaction.handleCropKeydown(event as KeyboardEvent));

    for (const [key, shiftKey, expected] of [
      ['ArrowLeft', false, [-8, 0]],
      ['ArrowRight', false, [8, 0]],
      ['ArrowUp', false, [0, -8]],
      ['ArrowDown', false, [0, 8]],
      ['ArrowLeft', true, [-32, 0]],
      ['ArrowRight', true, [32, 0]],
      ['ArrowUp', true, [0, -32]],
      ['ArrowDown', true, [0, 32]],
    ] as const) {
      const event = new KeyboardEvent('keydown', { key, shiftKey, cancelable: true, bubbles: true });
      viewport.dispatchEvent(event);
      expect(event.defaultPrevented).toBe(true);
      expect(onPan).toHaveBeenLastCalledWith(...expected);
    }

    const otherKey = new KeyboardEvent('keydown', { key: 'Enter', cancelable: true, bubbles: true });
    viewport.dispatchEvent(otherKey);
    expect(otherKey.defaultPrevented).toBe(false);
    expect(onPan).toHaveBeenCalledTimes(8);

    disabled = true;
    const disabledKey = new KeyboardEvent('keydown', { key: 'ArrowLeft', cancelable: true, bubbles: true });
    viewport.dispatchEvent(disabledKey);
    expect(disabledKey.defaultPrevented).toBe(false);
    expect(onPan).toHaveBeenCalledTimes(8);
    wrapper.unmount();
  });

  it('observes viewport resize and disconnects observer and window listeners', () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver);
    const { wrapper, interaction } = mountInteraction({
      isDisabled: () => false,
      onPan: () => undefined,
    });
    const onResize = vi.fn();
    const viewport = wrapper.element as HTMLElement;

    interaction.attachViewport(viewport, onResize);
    interaction.attachViewport(viewport, onResize);
    const observer = MockResizeObserver.latest;
    expect(observer?.observedElements).toEqual([viewport]);

    observer?.trigger();
    window.dispatchEvent(new Event('resize'));
    expect(onResize).toHaveBeenCalledTimes(2);

    wrapper.unmount();
    expect(observer?.disconnectCount).toBe(1);
    observer?.trigger();
    window.dispatchEvent(new Event('resize'));
    expect(onResize).toHaveBeenCalledTimes(2);
  });
});
