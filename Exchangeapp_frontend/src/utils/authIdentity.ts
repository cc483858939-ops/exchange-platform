export type AuthIdentity = {
  id: number;
  username: string;
  display_name: string;
  avatar_url: string;
};

type TokenClaims = {
  sub?: unknown;
};

const decodeBase64Url = (value: string): string => {
  const normalized = value.replace(/-/g, '+').replace(/_/g, '/');
  const padding = '='.repeat((4 - (normalized.length % 4)) % 4);
  const binary = atob(normalized + padding);
  const bytes = Uint8Array.from(binary, character => character.charCodeAt(0));
  return new TextDecoder().decode(bytes);
};

const readUserID = (value: unknown): number | null => {
  if (typeof value === 'number') {
    return Number.isSafeInteger(value) && value > 0 ? value : null;
  }
  if (typeof value === 'string' && /^\d+$/.test(value.trim())) {
    const parsed = Number(value);
    return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null;
  }
  return null;
};

export const normalizeAuthIdentity = (value: unknown): AuthIdentity | null => {
  if (typeof value !== 'object' || value === null) {
    return null;
  }
  const candidate = value as Record<string, unknown>;
  if (typeof candidate.id !== 'number' || !Number.isSafeInteger(candidate.id) || candidate.id <= 0 || typeof candidate.username !== 'string') {
    return null;
  }
  return {
    id: candidate.id,
    username: candidate.username,
    display_name: typeof candidate.display_name === 'string' ? candidate.display_name : '',
    avatar_url: typeof candidate.avatar_url === 'string' ? candidate.avatar_url : '',
  };
};

export const decodeAuthIdentity = (token: string | null | undefined): AuthIdentity | null => {
  try {
    const rawToken = token?.trim().replace(/^Bearer\s+/i, '');
    if (!rawToken) {
      return null;
    }
    const segments = rawToken.split('.');
    if (segments.length !== 3 || !segments[1]) {
      return null;
    }
    const claims = JSON.parse(decodeBase64Url(segments[1])) as TokenClaims;
    const id = readUserID(claims.sub);
    return id ? { id, username: '', display_name: '', avatar_url: '' } : null;
  } catch {
    return null;
  }
};
