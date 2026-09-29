// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import PostDraftExitDialog from './PostDraftExitDialog.vue';

describe('PostDraftExitDialog', () => {
  it('offers the correct three actions and handles busy and Escape states', async () => {
    const wrapper = mount(PostDraftExitDialog, {
      props: { isSavedDraft: false },
      attachTo: document.body,
    });
    const dialog = wrapper.get('dialog');
    expect(dialog.attributes('aria-modal')).toBe('true');
    expect(wrapper.get('h2').text()).toBe('Save post?');
    expect(wrapper.text()).toContain('Save this post as a draft before leaving?');
    expect(wrapper.findAll('button').map(button => button.text())).toEqual([
      'Cancel',
      'Discard',
      'Save draft',
    ]);
    expect(document.activeElement).toBe(wrapper.get('button').element);

    await wrapper.get('.post-draft-exit-dialog__button--discard').trigger('click');
    expect(wrapper.emitted('discard')).toHaveLength(1);
    await wrapper.get('.post-draft-exit-dialog__button--save').trigger('click');
    expect(wrapper.emitted('save')).toHaveLength(1);

    await wrapper.setProps({ isSavedDraft: true, busy: true });
    expect(wrapper.get('h2').text()).toBe('Save changes?');
    expect(wrapper.text()).toContain('Save your changes to this draft before leaving?');
    expect(wrapper.findAll('button').map(button => button.text())).toEqual([
      'Cancel',
      'Discard changes',
      'Saving…',
    ]);
    expect(dialog.attributes('aria-busy')).toBe('true');
    expect(wrapper.findAll('button').every(button => button.attributes('disabled') !== undefined)).toBe(true);

    const escape = new Event('cancel', { cancelable: true });
    dialog.element.dispatchEvent(escape);
    expect(escape.defaultPrevented).toBe(true);
    expect(wrapper.emitted('cancel')).toBeUndefined();
    wrapper.unmount();
  });
});
