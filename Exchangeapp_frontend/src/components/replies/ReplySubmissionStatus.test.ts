// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import ReplySubmissionStatus from './ReplySubmissionStatus.vue';

describe('ReplySubmissionStatus', () => {
  it('shows a retry only for a retryable failure', async () => {
    const wrapper = mount(ReplySubmissionStatus, {
      props: { operation: { phase: 'failed', failureKind: 'retryable' } },
    });
    expect(wrapper.text()).toContain('Reply failed. Retry safely.');
    await wrapper.get('button').trigger('click');
    expect(wrapper.emitted('retry')).toHaveLength(1);
    await wrapper.get('.reply-submission-status__action--quiet').trigger('click');
    expect(wrapper.emitted('discard')).toHaveLength(1);
  });

  it('does not expose Retry for idempotency conflicts', () => {
    const wrapper = mount(ReplySubmissionStatus, {
      props: { operation: { phase: 'failed', failureKind: 'idempotency_conflict' } },
    });
    expect(wrapper.text()).toContain('This reply can’t be retried safely.');
    expect(wrapper.text()).toContain('Discard attempt');
    expect(wrapper.find('button').exists()).toBe(true);
    expect(wrapper.findAll('button')).toHaveLength(1);
  });

  it('distinguishes in-flight and cleanup-pending success states', async () => {
    const wrapper = mount(ReplySubmissionStatus, {
      props: { operation: { phase: 'publishing', failureKind: null } },
    });
    expect(wrapper.text()).toContain('Sending reply…');
    await wrapper.setProps({ operation: { phase: 'succeeded', failureKind: null, cleanupPending: true } });
    expect(wrapper.text()).toContain('Finishing draft cleanup');
    expect(wrapper.find('button').exists()).toBe(false);
  });
});
