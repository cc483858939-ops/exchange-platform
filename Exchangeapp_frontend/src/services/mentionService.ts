import axios from 'axios';
import apiClient from '../axios';
import type { PublicUserSummary } from '../types/User';

export type MentionSuggestion = {
  id: number;
  username: string;
  display_name: string;
  avatar_url: string;
  reason: 'recently_mentioned' | 'following';
};

type MentionSuggestionsResponse = {
  items: Array<{
    user: Omit<MentionSuggestion, 'reason'>;
    reason: MentionSuggestion['reason'];
  }>;
};

export async function getMentionSuggestions(
  prefix: string,
  signal?: AbortSignal,
): Promise<MentionSuggestion[]> {
  const response = await apiClient.get<MentionSuggestionsResponse>(
    '/me/mention-suggestions',
    { params: { prefix, limit: 8 }, signal },
  );
  const seen = new Set<number>();
  return response.data.items.flatMap(({ user, reason }) => {
    if (seen.has(user.id)) {
      return [];
    }
    seen.add(user.id);
    return [{ ...user, reason }];
  });
}

export async function resolveMentionUsername(username: string, signal?: AbortSignal) {
  try {
    const response = await apiClient.get<PublicUserSummary>(
      `/users/by-username/${encodeURIComponent(username)}`,
      { signal },
    );
    return response.data;
  } catch (error) {
    if (axios.isAxiosError(error) && error.response?.status === 404) {
      return null;
    }
    throw error;
  }
}
