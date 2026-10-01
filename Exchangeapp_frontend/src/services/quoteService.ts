import apiClient from '../axios';
import type { PostQuotePageResponse } from '../types/Post';
import { normalizeResourceID } from './resourceId';

export type QuoteQuery = {
  limit?: number;
  cursor?: string;
};

export async function getPostQuotes(
  postID: number | string,
  options: QuoteQuery = {},
): Promise<PostQuotePageResponse> {
  const id = normalizeResourceID(postID, 'post');
  const response = await apiClient.get<PostQuotePageResponse>(`/posts/${id}/quotes`, { params: options });
  return response.data;
}
