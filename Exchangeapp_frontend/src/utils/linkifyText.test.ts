import { describe, expect, it } from 'vitest';
import { linkifyText } from './linkifyText';

describe('linkifyText', () => {
  it('splits an HTTPS URL from surrounding text', () => {
    expect(linkifyText('hello https://example.com world')).toEqual([
      { type: 'text', text: 'hello ' },
      { type: 'link', text: 'https://example.com', href: 'https://example.com' },
      { type: 'text', text: ' world' },
    ]);
  });

  it('supports HTTP URLs without changing their href', () => {
    expect(linkifyText('http://example.com')).toEqual([
      { type: 'link', text: 'http://example.com', href: 'http://example.com' },
    ]);
  });

  it('normalizes www URLs to HTTPS while preserving their display text', () => {
    expect(linkifyText('www.example.com/test')).toEqual([
      { type: 'link', text: 'www.example.com/test', href: 'https://www.example.com/test' },
    ]);
  });

  it('finds multiple URLs in one text value', () => {
    const segments = linkifyText('https://a.com and https://b.com');

    expect(segments.filter(segment => segment.type === 'link')).toEqual([
      { type: 'link', text: 'https://a.com', href: 'https://a.com' },
      { type: 'link', text: 'https://b.com', href: 'https://b.com' },
    ]);
  });

  it('keeps English sentence punctuation outside the link', () => {
    expect(linkifyText('https://example.com.')).toEqual([
      { type: 'link', text: 'https://example.com', href: 'https://example.com' },
      { type: 'text', text: '.' },
    ]);
  });

  it('keeps Chinese sentence punctuation outside the link', () => {
    expect(linkifyText('链接：https://example.com。')).toEqual([
      { type: 'text', text: '链接：' },
      { type: 'link', text: 'https://example.com', href: 'https://example.com' },
      { type: 'text', text: '。' },
    ]);
  });

  it('preserves query strings and fragments', () => {
    expect(linkifyText('https://example.com?q=test&id=1')).toEqual([
      {
        type: 'link',
        text: 'https://example.com?q=test&id=1',
        href: 'https://example.com?q=test&id=1',
      },
    ]);
    expect(linkifyText('https://example.com/docs#part-2')).toEqual([
      {
        type: 'link',
        text: 'https://example.com/docs#part-2',
        href: 'https://example.com/docs#part-2',
      },
    ]);
  });

  it('does not link plain domains', () => {
    expect(linkifyText('example.com')).toEqual([
      { type: 'text', text: 'example.com' },
    ]);
  });

  it('preserves newlines and HTML-looking input as text', () => {
    expect(linkifyText('<script>alert(1)</script>\nnext line')).toEqual([
      { type: 'text', text: '<script>alert(1)</script>\nnext line' },
    ]);
  });
});
