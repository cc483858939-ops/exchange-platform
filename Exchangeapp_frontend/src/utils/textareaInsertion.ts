export interface TextareaInsertionResult {
  value: string;
  caret: number;
}

const isValidOffset = (offset: number | null | undefined, length: number): offset is number => (
  typeof offset === 'number'
  && Number.isInteger(offset)
  && offset >= 0
  && offset <= length
);

export const insertTextAtSelection = (
  value: string,
  text: string,
  selectionStart?: number | null,
  selectionEnd?: number | null,
): TextareaInsertionResult => {
  const fallback = value.length;
  const start = isValidOffset(selectionStart, value.length) ? selectionStart : fallback;
  const candidateEnd = isValidOffset(selectionEnd, value.length) ? selectionEnd : start;
  const end = Math.max(start, candidateEnd);

  return {
    value: value.slice(0, start) + text + value.slice(end),
    caret: start + text.length,
  };
};
