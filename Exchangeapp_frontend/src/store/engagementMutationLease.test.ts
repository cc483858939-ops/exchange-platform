import { describe, expect, it } from 'vitest';
import { createEngagementMutationLeaseRegistry } from './engagementMutationLease';

describe('engagement mutation lease registry', () => {
  it('denies a second owner for the same viewer, kind, and post', () => {
    const registry = createEngagementMutationLeaseRegistry();

    expect(registry.tryBegin(7, 'like', 42)).not.toBeNull();
    expect(registry.tryBegin(7, 'like', 42)).toBeNull();
  });

  it('allows different interaction kinds for the same post', () => {
    const registry = createEngagementMutationLeaseRegistry();

    expect(registry.tryBegin(7, 'like', 42)).not.toBeNull();
    expect(registry.tryBegin(7, 'repost', 42)).not.toBeNull();
    expect(registry.tryBegin(7, 'bookmark', 42)).not.toBeNull();
  });

  it('allows the same interaction kind for different posts', () => {
    const registry = createEngagementMutationLeaseRegistry();

    expect(registry.tryBegin(7, 'like', 42)).not.toBeNull();
    expect(registry.tryBegin(7, 'like', 43)).not.toBeNull();
  });

  it('allows different viewers to mutate the same post and kind concurrently', () => {
    const registry = createEngagementMutationLeaseRegistry();

    expect(registry.tryBegin(7, 'like', 42)).not.toBeNull();
    expect(registry.tryBegin(8, 'like', 42)).not.toBeNull();
  });

  it('allows reacquiring after the current owner releases', () => {
    const registry = createEngagementMutationLeaseRegistry();
    const first = registry.tryBegin(7, 'like', 42);

    expect(first).not.toBeNull();
    expect(registry.release(first!)).toBe(true);
    expect(registry.tryBegin(7, 'like', 42)).not.toBeNull();
  });

  it('does not let a stale token release a later owner', () => {
    const registry = createEngagementMutationLeaseRegistry();
    const first = registry.tryBegin(7, 'like', 42)!;
    expect(registry.release(first)).toBe(true);
    const second = registry.tryBegin(7, 'like', 42)!;

    expect(registry.release(first)).toBe(false);
    expect(registry.isLeased(7, 'like', 42)).toBe(true);
    expect(registry.release(second)).toBe(true);
    expect(registry.isLeased(7, 'like', 42)).toBe(false);
  });

  it('rejects invalid viewer and post identifiers without leasing them', () => {
    const registry = createEngagementMutationLeaseRegistry();

    for (const id of [0, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(registry.tryBegin(id, 'like', 42)).toBeNull();
      expect(registry.tryBegin(7, 'like', id)).toBeNull();
    }
    expect(registry.tryBegin(7, 'invalid' as never, 42)).toBeNull();
    expect(registry.isLeased(7, 'like', 42)).toBe(false);
  });
});
