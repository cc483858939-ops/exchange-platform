import { describe, expect, it } from 'vitest';
import { extractMentionTokens, mentionAtCaret, replaceMentionAtCaret } from './mentionText';

describe('mentionText', () => {
  it('recognizes multiple names, Unicode usernames, and mentions after CJK text', () => {
    expect(extractMentionTokens('你好 @alice 和 @用户_甲-2')).toEqual([
      { username: 'alice', start: 3, end: 9 },
      { username: '用户_甲-2', start: 12, end: 19 },
    ]);
  });

  it('does not parse emails, URL handles, doubled at signs, or Latin word suffixes', () => {
    expect(extractMentionTokens('a@alice.com @@bob word@carol https://x.test/@dana www.site.test/@erin'))
      .toEqual([]);
  });

  it('preserves exact codepoint limits and rejects dotted handle prefixes', () => {
    const accepted = 'a'.repeat(64);
    expect(extractMentionTokens(`@${accepted}`)).toHaveLength(1);
    expect(extractMentionTokens(`@${accepted}a`)).toEqual([]);
    expect(extractMentionTokens('@alice.example')).toEqual([]);
  });

  it('finds the active token at the caret and includes its suffix for replacement', () => {
    const text = '🌱 hi @alice, welcome';
    const caret = text.indexOf('@alice') + 4;
    expect(mentionAtCaret(text, caret, caret)).toEqual({
      username: 'alice',
      prefix: 'ali',
      start: text.indexOf('@alice'),
      end: text.indexOf('@alice') + '@alice'.length,
    });
  });

  it('keeps emoji offsets in UTF-16 and replaces only the current mention', () => {
    const text = '🌱 hi @alX rest\nnext';
    const caret = text.indexOf('@alX') + 3;
    const context = mentionAtCaret(text, caret, caret);
    expect(context?.prefix).toBe('al');
    expect(replaceMentionAtCaret(text, context!, 'alice')).toEqual({
      value: '🌱 hi @alice rest\nnext',
      caret: text.indexOf('@alX') + '@alice'.length,
    });
  });

  it('does not activate on selections or when the caret is outside a mention', () => {
    expect(mentionAtCaret('@alice', 2, 3)).toBeNull();
    expect(mentionAtCaret('@alice text', 10, 10)).toBeNull();
    expect(mentionAtCaret('mail@alice.com', 8, 8)).toBeNull();
  });
});
