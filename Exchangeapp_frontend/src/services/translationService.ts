import apiClient from '../axios';
import type { PostLanguage } from '../types/Post';
import {
  normalizeTranslationLanguage,
  type TranslationLanguage,
} from '../utils/translationLanguage';

export type { TranslationLanguage } from '../utils/translationLanguage';

export interface PostTranslationResponse {
  post_id: number;
  source_language: PostLanguage;
  target_language: TranslationLanguage;
  translated: boolean;
  translation: string;
}

export async function translatePost(
  postID: number | string,
  targetLanguage: string,
): Promise<PostTranslationResponse> {
  const normalizedTarget = normalizeTranslationLanguage(targetLanguage);
  if (!normalizedTarget) {
    throw new Error('Invalid translation target language');
  }

  const rawID = typeof postID === 'number' ? String(postID) : postID.trim();
  const parsedID = Number(rawID);
  if (!rawID || !Number.isSafeInteger(parsedID) || parsedID <= 0) {
    throw new Error('Invalid post id');
  }

  const response = await apiClient.post<PostTranslationResponse>(
    `/posts/${parsedID}/translation`,
    { target_language: normalizedTarget },
  );
  return response.data;
}
