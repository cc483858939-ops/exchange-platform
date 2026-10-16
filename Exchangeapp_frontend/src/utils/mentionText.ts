export type MentionToken = {
  username: string;
  start: number;
  end: number;
};

export type MentionCaretContext = MentionToken & {
  prefix: string;
};

const MAX_USERNAME_CODEPOINTS = 64;
const URL_BOUNDARIES = new Set(Array.from("([{<\"'"));

const usernameCharacter = (value: string | undefined) => (
  value !== undefined && /^[\p{L}\p{N}\p{M}_-]$/u.test(value)
);

const isCJKLetter = (value: string) => {
  const codepoint = value.codePointAt(0) ?? 0;
  return (codepoint >= 0x3400 && codepoint <= 0x4dbf)
    || (codepoint >= 0x4e00 && codepoint <= 0x9fff)
    || (codepoint >= 0xf900 && codepoint <= 0xfaff)
    || (codepoint >= 0x3040 && codepoint <= 0x30ff)
    || (codepoint >= 0xac00 && codepoint <= 0xd7af);
};

const validAtBoundary = (characters: string[], at: number) => {
  if (at === 0) {
    return true;
  }
  const previous = characters[at - 1] ?? '';
  if (previous === '@' || previous === '_' || previous === '-' || /^[\p{N}\p{M}]$/u.test(previous)) {
    return false;
  }
  return !/^\p{L}$/u.test(previous) || isCJKLetter(previous);
};

const isInUrl = (characters: string[], at: number) => {
  let start = at;
  while (start > 0) {
    const previous = characters[start - 1] ?? '';
    if (/^\s$/u.test(previous) || URL_BOUNDARIES.has(previous)) {
      break;
    }
    start -= 1;
  }

  const segment = characters.slice(start, at).join('').toLowerCase();
  if (segment.includes('://') || segment.startsWith('www.')) {
    return true;
  }
  const colon = segment.indexOf(':');
  if (colon > 0 && /^[a-z][a-z0-9+.-]*$/u.test(segment.slice(0, colon))) {
    return true;
  }
  const hostEnd = segment.search(/[/?#]/u);
  return segment.slice(0, hostEnd < 0 ? segment.length : hostEnd).includes('.');
};

const codepointOffsets = (characters: string[]) => {
  const offsets = [0];
  for (const character of characters) {
    offsets.push(offsets[offsets.length - 1]! + character.length);
  }
  return offsets;
};

const codepointIndexAt = (offsets: number[], utf16Offset: number) => {
  let low = 0;
  let high = offsets.length - 1;
  while (low < high) {
    const middle = Math.ceil((low + high) / 2);
    if (offsets[middle]! <= utf16Offset) {
      low = middle;
    } else {
      high = middle - 1;
    }
  }
  return low;
};

const usernameEndAt = (characters: string[], start: number) => {
  let end = start;
  while (end < characters.length && usernameCharacter(characters[end])) {
    end += 1;
  }
  if (end === start) {
    return null;
  }
  if (characters[end] === '.' && usernameCharacter(characters[end + 1])) {
    return null;
  }
  if (end - start > MAX_USERNAME_CODEPOINTS) {
    return null;
  }
  return end;
};

export function extractMentionTokens(text: string): MentionToken[] {
  const characters = Array.from(text);
  const offsets = codepointOffsets(characters);
  const mentions: MentionToken[] = [];

  for (let at = 0; at < characters.length; at += 1) {
    if (
      characters[at] !== '@'
      || at + 1 >= characters.length
      || !validAtBoundary(characters, at)
      || isInUrl(characters, at)
    ) {
      continue;
    }
    const end = usernameEndAt(characters, at + 1);
    if (end === null) {
      continue;
    }
    mentions.push({
      username: characters.slice(at + 1, end).join(''),
      start: offsets[at]!,
      end: offsets[end]!,
    });
    at = end - 1;
  }

  return mentions;
}

export function mentionAtCaret(
  text: string,
  selectionStart: number,
  selectionEnd: number,
): MentionCaretContext | null {
  if (selectionStart !== selectionEnd) {
    return null;
  }

  const characters = Array.from(text);
  const offsets = codepointOffsets(characters);
  const caret = codepointIndexAt(offsets, selectionStart);

  for (let at = 0; at < characters.length && at < caret; at += 1) {
    if (
      characters[at] !== '@'
      || !validAtBoundary(characters, at)
      || isInUrl(characters, at)
    ) {
      continue;
    }

    let end = at + 1;
    while (end < characters.length && usernameCharacter(characters[end])) {
      end += 1;
    }
    if (
      (end === at + 1 && caret !== at + 1)
      || (characters[end] === '.' && usernameCharacter(characters[end + 1]))
      || end - at - 1 > MAX_USERNAME_CODEPOINTS
      || caret < at + 1
      || caret > end
    ) {
      continue;
    }

    const prefix = characters.slice(at + 1, caret).join('');
    if (Array.from(prefix).some(character => !usernameCharacter(character))) {
      continue;
    }

    return {
      username: characters.slice(at + 1, end).join(''),
      prefix,
      start: offsets[at]!,
      end: offsets[end]!,
    };
  }

  return null;
}

export function replaceMentionAtCaret(
  text: string,
  context: MentionCaretContext,
  username: string,
) {
  const inserted = `@${username}`;
  const value = text.slice(0, context.start) + inserted + text.slice(context.end);
  const caret = context.start + inserted.length;
  return { value, caret };
}
