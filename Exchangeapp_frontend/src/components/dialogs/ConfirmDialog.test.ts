// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import ConfirmDialog from './ConfirmDialog.vue';

const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
const originalClose = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'close');
let showModalMock: ReturnType<typeof vi.fn>;
let closeMock: ReturnType<typeof vi.fn>;
const mountedDialogs: Array<ReturnType<typeof mount>> = [];

beforeEach(() => {
  showModalMock = vi.fn(function showModal(this: HTMLDialogElement) {
    this.setAttribute('open', '');
  });
  closeMock = vi.fn(function close(this: HTMLDialogElement) {
    this.removeAttribute('open');
  });
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value: showModalMock,
  });
  Object.defineProperty(HTMLDialogElement.prototype, 'close', {
    configurable: true,
    value: closeMock,
  });
});

afterEach(() => {
  mountedDialogs.splice(0).forEach(wrapper => wrapper.unmount());
  if (originalShowModal) {
    Object.defineProperty(HTMLDialogElement.prototype, 'showModal', originalShowModal);
  } else {
    Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
  }
  if (originalClose) {
    Object.defineProperty(HTMLDialogElement.prototype, 'close', originalClose);
  } else {
    Reflect.deleteProperty(HTMLDialogElement.prototype, 'close');
  }
});

const mountDialog = (props: Record<string, unknown> = {}) => {
  const wrapper = mount(ConfirmDialog, {
    attachTo: document.body,
    props: {
      title: 'Delete reply?',
      description: 'This reply will be permanently deleted. This can’t be undone.',
      confirmLabel: 'Delete',
      cancelLabel: 'Cancel',
      danger: true,
      ...props,
    },
  });
  mountedDialogs.push(wrapper);
  return wrapper;
};

describe('ConfirmDialog', () => {
  it('opens natively and exposes the requested copy', () => {
    const wrapper = mountDialog();

    expect(showModalMock).toHaveBeenCalledTimes(1);
    expect(wrapper.get('dialog').attributes('open')).toBe('');
    expect(wrapper.get('.confirm-dialog__title').text()).toBe('Delete reply?');
    expect(wrapper.get('.confirm-dialog__description').text())
      .toBe('This reply will be permanently deleted. This can’t be undone.');
  });

  it('focuses Cancel initially', () => {
    const focusSpy = vi.spyOn(HTMLButtonElement.prototype, 'focus');
    const wrapper = mountDialog();
    const cancelButton = wrapper.get('.confirm-dialog__button--cancel').element;

    expect(focusSpy.mock.contexts).toContain(cancelButton);
    focusSpy.mockRestore();
  });

  it('emits cancel from the Cancel button and idle Escape', async () => {
    const wrapper = mountDialog();

    await wrapper.get('.confirm-dialog__button--cancel').trigger('click');
    await wrapper.get('dialog').trigger('cancel');

    expect(wrapper.emitted('cancel')).toHaveLength(2);
  });

  it('emits confirm from the Delete button', async () => {
    const wrapper = mountDialog();

    await wrapper.get('.confirm-dialog__button--confirm').trigger('click');

    expect(wrapper.emitted('confirm')).toHaveLength(1);
  });

  it('disables both actions and prevents Escape while busy', async () => {
    const wrapper = mountDialog({ busy: true });

    expect(wrapper.get('.confirm-dialog__button--cancel').attributes('disabled')).toBe('');
    expect(wrapper.get('.confirm-dialog__button--confirm').attributes('disabled')).toBe('');
    expect(wrapper.get('.confirm-dialog__button--confirm').text()).toBe('Deleting…');

    await wrapper.get('dialog').trigger('cancel');
    await wrapper.get('.confirm-dialog__button--cancel').trigger('click');
    await wrapper.get('.confirm-dialog__button--confirm').trigger('click');

    expect(wrapper.emitted('cancel')).toBeUndefined();
    expect(wrapper.emitted('confirm')).toBeUndefined();
  });

  it('announces an error accessibly', () => {
    const wrapper = mountDialog({ error: 'Reply could not be deleted. Please try again.' });

    expect(wrapper.get('[role="alert"]').text()).toBe('Reply could not be deleted. Please try again.');
    expect(wrapper.get('dialog').attributes('aria-describedby')).toBe('confirm-dialog-description');
  });

  it('closes the native dialog on unmount', () => {
    const wrapper = mountDialog();

    wrapper.unmount();

    expect(closeMock).toHaveBeenCalledTimes(1);
  });
});
