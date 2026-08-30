import type { FeedPost } from '../types/Feed';
import type { PublicAuthor } from '../types/User';
import type { PerfFixture } from './types';

const fixtureAuthors: PublicAuthor[] = [
  { id: 9001, username: 'perf_alex', display_name: 'Alex Chen', avatar_url: '' },
  { id: 9002, username: 'perf_mina', display_name: 'Mina Park', avatar_url: '' },
  { id: 9003, username: 'perf_jo', display_name: 'Jo Rivera', avatar_url: '' },
  { id: 9004, username: 'perf_sam', display_name: 'Sam Okafor', avatar_url: '' },
];

const baseTimestamp = Date.parse('2026-01-01T00:00:00.000Z');
const longExcerpt = 'A deterministic long-form excerpt keeps the real PostCard text layout exercised without relying on remote content, clocks, or random data. It contains enough words to reach the same clamping and wrapping paths repeatedly across every benchmark run while remaining stable on every machine.';
const coverSvg = '<svg xmlns="http://www.w3.org/2000/svg" width="160" height="90" viewBox="0 0 160 90"><rect width="160" height="90" fill="#dbeafe"/><path d="M0 70 38 38l22 18 24-28 76 42v20H0Z" fill="#93c5fd"/><circle cx="116" cy="26" r="12" fill="#fbbf24"/></svg>';
const coverDataURI = `data:image/svg+xml,${encodeURIComponent(coverSvg)}`;

const excerptFor = (position: number, postID: number): string => {
  switch (position % 3) {
    case 1:
      return `A short deterministic note for benchmark post ${postID}.`;
    case 2:
      return `A medium deterministic post ${postID} keeps a few lines of realistic text in the mounted card so wrapping and clamping remain representative.`;
    default:
      return longExcerpt;
  }
};

const normalizedCount = (count: number): number => (
  Number.isSafeInteger(count) && count > 0 ? count : 0
);

const normalizedStartID = (startID: number): number => (
  Number.isSafeInteger(startID) && startID > 0 ? startID : 1
);

export function createPerfPosts(
  count: number,
  fixture: PerfFixture = 'mixed',
  startID = 1,
): FeedPost[] {
  const total = normalizedCount(count);
  const firstID = normalizedStartID(startID);

  return Array.from({ length: total }, (_, index) => {
    const position = index + 1;
    const id = firstID + index;
    const author = fixtureAuthors[index % fixtureAuthors.length];
    const hasHeadline = fixture === 'mixed' && position % 2 === 0;
    const hasCover = fixture === 'mixed' && position % 3 === 0;
    const hasRepostContext = fixture === 'mixed' && position % 5 === 0;

    return {
      id,
      author: { ...author },
      title: hasHeadline ? `Deterministic headline ${id}` : '',
      excerpt: excerptFor(position, id),
      coverImageUrl: hasCover ? coverDataURI : '',
      createdAt: new Date(baseTimestamp + index * 60_000).toISOString(),
      likeCount: (position * 7) % 97,
      commentCount: (position * 5) % 31,
      viewCount: position * 113,
      liked: position % 7 === 0,
      likeStatus: 'ready',
      repostCount: (position * 3) % 19,
      reposted: position % 11 === 0,
      repostStatus: 'ready',
      repostContext: hasRepostContext
        ? { actor: { ...fixtureAuthors[(index + 1) % fixtureAuthors.length] } }
        : undefined,
    } satisfies FeedPost;
  });
}

export { coverDataURI };
