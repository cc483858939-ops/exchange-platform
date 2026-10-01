import { describe, expect, it } from 'vitest';
import {
  composerIntentsEqual,
  composerIntentKey,
  parseComposerIntent,
} from './composerIntent';

describe('composer intent parsing', () => {
  it('gives a valid saved draft priority over a quote query', () => {
    expect(parseComposerIntent({ draft: ' draft-1 ', quote: '42' }))
      .toEqual({ kind: 'draft', draftID: 'draft-1' });
  });

  it.each([
    ['abc'],
    ['0'],
    ['-1'],
    ['1.5'],
    ['9007199254740992'],
    [['42', '43']],
  ])('normalizes invalid quote query %s to a new composer', quote => {
    expect(parseComposerIntent({ quote })).toEqual({ kind: 'new' });
  });

  it('parses only positive safe integer quote IDs', () => {
    expect(parseComposerIntent({ quote: '42' })).toEqual({ kind: 'quote', postID: 42 });
    expect(composerIntentKey({ kind: 'quote', postID: 42 })).toBe('quote:42');
    expect(composerIntentsEqual(
      { kind: 'quote', postID: 42 },
      { kind: 'quote', postID: 43 },
    )).toBe(false);
    expect(composerIntentsEqual(
      { kind: 'draft', draftID: 'same' },
      { kind: 'draft', draftID: 'same' },
    )).toBe(true);
  });
});
