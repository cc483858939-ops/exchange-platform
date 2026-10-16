import { extractMentionTokens } from './mentionText';

export type LinkifiedTextSegment =
  | {
      type: 'text';
      text: string;
    }
  | {
      type: 'link';
      text: string;
      href: string;
    }
  | {
      type: 'mention';
      text: string;
      username: string;
    };

const URL_PATTERN = /(?:https?:\/\/|www\.)[^\s<>"'`]+/gi;
const TRAILING_PUNCTUATION = new Set([
  '.',
  ',',
  '!',
  '?',
  ':',
  ';',
  '。',
  '，',
  '！',
  '？',
  '：',
  '；',
]);

const isUrlStart = (text: string, index: number) => (
  index === 0 || !/[A-Za-z0-9._-]/.test(text[index - 1] ?? '')
);

const splitTrailingPunctuation = (value: string) => {
  let urlEnd = value.length;
  while (urlEnd > 0 && TRAILING_PUNCTUATION.has(value[urlEnd - 1] ?? '')) {
    urlEnd -= 1;
  }

  return {
    url: value.slice(0, urlEnd),
    trailing: value.slice(urlEnd),
  };
};

export function linkifyText(text: string): LinkifiedTextSegment[] {
  if (!text) {
    return [];
  }

  const segments: LinkifiedTextSegment[] = [];
  let cursor = 0;

  for (const match of text.matchAll(URL_PATTERN)) {
    const matchedText = match[0];
    const start = match.index ?? 0;
    if (!isUrlStart(text, start)) {
      continue;
    }

    const { url, trailing } = splitTrailingPunctuation(matchedText);
    if (!url) {
      continue;
    }

    if (start > cursor) {
      segments.push({ type: 'text', text: text.slice(cursor, start) });
    }

    segments.push({
      type: 'link',
      text: url,
      href: url.toLowerCase().startsWith('www.') ? `https://${url}` : url,
    });

    if (trailing) {
      segments.push({ type: 'text', text: trailing });
    }

    cursor = start + matchedText.length;
  }

  if (cursor < text.length) {
    segments.push({ type: 'text', text: text.slice(cursor) });
  }

  const urlSegments = segments.length > 0 ? segments : [{ type: 'text' as const, text }];
  return urlSegments.flatMap(segment => {
    if (segment.type !== 'text') {
      return [segment];
    }
    const mentions = extractMentionTokens(segment.text);
    if (mentions.length === 0) {
      return [segment];
    }
    const parts: LinkifiedTextSegment[] = [];
    let mentionCursor = 0;
    for (const mention of mentions) {
      if (mention.start > mentionCursor) {
        parts.push({ type: 'text', text: segment.text.slice(mentionCursor, mention.start) });
      }
      parts.push({
        type: 'mention',
        text: segment.text.slice(mention.start, mention.end),
        username: mention.username,
      });
      mentionCursor = mention.end;
    }
    if (mentionCursor < segment.text.length) {
      parts.push({ type: 'text', text: segment.text.slice(mentionCursor) });
    }
    return parts;
  });
}
