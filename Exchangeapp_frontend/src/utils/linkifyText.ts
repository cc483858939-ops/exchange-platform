export type LinkifiedTextSegment =
  | {
      type: 'text';
      text: string;
    }
  | {
      type: 'link';
      text: string;
      href: string;
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

  return segments.length > 0 ? segments : [{ type: 'text', text }];
}
