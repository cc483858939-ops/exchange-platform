// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import ReplyDraftExitDialog from './ReplyDraftExitDialog.vue';

describe('ReplyDraftExitDialog', () => {
  it('offers Save, Discard, and Cancel for a new reply draft', async () => {
    const wrapper = mount(ReplyDraftExitDialog, { props: { isSavedDraft: false } });
    expect(wrapper.get('h2').text()).toBe('Save reply?');
    expect(wrapper.get('p.reply-draft-exit-dialog__description').text())
      .toBe('Save this reply as a draft before leaving?');
    expect(wrapper.findAll('.reply-draft-exit-dialog__button').map(button => button.text()))
      .toEqual(['Cancel', 'Discard', 'Save draft']);

    await wrapper.get('.reply-draft-exit-dialog__button--cancel').trigger('click');
    await wrapper.get('.reply-draft-exit-dialog__button--discard').trigger('click');
    await wrapper.get('.reply-draft-exit-dialog__button--save').trigger('click');
    expect(wrapper.emitted('cancel')).toHaveLength(1);
    expect(wrapper.emitted('discard')).toHaveLength(1);
    expect(wrapper.emitted('save')).toHaveLength(1);
    wrapper.unmount();
  });

  it('uses saved-draft copy and displays save errors without losing the actions', () => {
    const wrapper = mount(ReplyDraftExitDialog, {
      props: { isSavedDraft: true, error: 'Could not save this reply draft.' },
    });
    expect(wrapper.get('h2').text()).toBe('Save changes?');
    expect(wrapper.get('.reply-draft-exit-dialog__button--discard').text()).toBe('Discard changes');
    expect(wrapper.get('.reply-draft-exit-dialog__button--save').text()).toBe('Save');
    expect(wrapper.get('[role="alert"]').text()).toBe('Could not save this reply draft.');
    wrapper.unmount();
  });

  it('locks all exit actions while Save is pending', () => {
    const wrapper = mount(ReplyDraftExitDialog, { props: { isSavedDraft: false, busy: true } });
    expect(wrapper.findAll('button:disabled')).toHaveLength(3);
    expect(wrapper.get('.reply-draft-exit-dialog__button--save').text()).toBe('Saving…');
    wrapper.unmount();
  });
});
