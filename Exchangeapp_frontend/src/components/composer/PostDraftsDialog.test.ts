// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import PostDraftsDialog from './PostDraftsDialog.vue';
import type { PersistedPostDraft } from '../../storage/postDraftRepository';

const makeDraft = (id: string, updatedAt: number, content: string, mediaCount = 0): PersistedPostDraft => ({
  id,
  viewerID: 7,
  content,
  quotePostID: null,
  media: Array.from({ length: mediaCount }, (_, index) => ({
    id: `${id}-media-${index}`,
    blob: new Blob(['image'], { type: 'image/png' }),
    name: `image-${index}.png`,
    type: 'image/png',
    size: 5,
    lastModified: 1,
    uploadedURL: '',
  })),
  createdAt: updatedAt - 100,
  updatedAt,
});

describe('PostDraftsDialog', () => {
  it('lists drafts newest first with previews and Open/Delete actions', async () => {
    const wrapper = mount(PostDraftsDialog, {
      props: {
        drafts: [
          makeDraft('media-draft', 100, '', 2),
          makeDraft('newest', 300, 'Newest post'),
          makeDraft('older', 200, 'An older post'),
        ],
      },
      attachTo: document.body,
    });

    expect(wrapper.get('dialog').attributes('aria-modal')).toBe('true');
    expect(wrapper.findAll('.post-drafts-dialog__item').map(item => item.text())).toHaveLength(3);
    const items = wrapper.findAll('.post-drafts-dialog__item');
    expect(items[0]?.text()).toContain('Newest post');
    expect(items[1]?.text()).toContain('An older post');
    expect(items[2]?.text()).toContain('Media post');
    expect(items[2]?.text()).toContain('2 images');

    await items[0]?.get('.post-drafts-dialog__button--open').trigger('click');
    await items[1]?.get('.post-drafts-dialog__button--delete').trigger('click');
    expect(wrapper.emitted('open-draft')).toEqual([['newest']]);
    expect(wrapper.emitted('delete-draft')).toEqual([['older']]);

    await wrapper.setProps({ busy: true });
    expect(wrapper.findAll('button').every(button => button.attributes('disabled') !== undefined)).toBe(true);
    wrapper.unmount();
  });
});
