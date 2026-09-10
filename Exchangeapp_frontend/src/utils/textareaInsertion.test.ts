import { describe, expect, it } from 'vitest';
import { insertTextAtSelection } from './textareaInsertion';

describe('insertTextAtSelection', () => {
  it('inserts text at a caret in the middle of a value', () => {
    expect(insertTextAtSelection('hello world', '😂', 6, 6)).toEqual({
      value: 'hello 😂world',
      caret: 8,
    });
  });

  it('replaces the selected range', () => {
    const emoji = '❤️';

    expect(insertTextAtSelection('hello bad world', emoji, 6, 9)).toEqual({
      value: 'hello ❤️ world',
      caret: 6 + emoji.length,
    });
  });

  it('appends when the selection is unavailable', () => {
    expect(insertTextAtSelection('hello', '🔥', null, null)).toEqual({
      value: 'hello🔥',
      caret: 'hello🔥'.length,
    });
  });

  it.each(['😂', '👍🏽', '❤️', '👨‍👩‍👧‍👦'])(
    'keeps the complete multi-code-unit emoji sequence %s',
    emoji => {
      const result = insertTextAtSelection('x', emoji, 1, 1);

      expect(result.value).toBe('x' + emoji);
      expect(result.caret).toBe(1 + emoji.length);
    },
  );
});
