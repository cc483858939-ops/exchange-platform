export const GUEST_RECOMMENDATION_SESSION_STORAGE_KEY =
  'exchange.guest-recommendation-session.v1';

const guestRecommendationSessionPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export const isValidGuestRecommendationSessionID = (value: string): boolean =>
  guestRecommendationSessionPattern.test(value.trim());

export const getGuestRecommendationSessionID = (): string | null => {
  if (typeof window === 'undefined') {
    return null;
  }

  try {
    const existing = window.sessionStorage.getItem(
      GUEST_RECOMMENDATION_SESSION_STORAGE_KEY,
    );
    const normalizedExisting = existing?.trim() ?? '';
    if (normalizedExisting && isValidGuestRecommendationSessionID(normalizedExisting)) {
      if (existing !== normalizedExisting) {
        window.sessionStorage.setItem(
          GUEST_RECOMMENDATION_SESSION_STORAGE_KEY,
          normalizedExisting,
        );
      }
      return normalizedExisting;
    }

    const cryptoAPI = window.crypto;
    if (!cryptoAPI || typeof cryptoAPI.randomUUID !== 'function') {
      return null;
    }

    const generated = cryptoAPI.randomUUID().trim();
    if (!isValidGuestRecommendationSessionID(generated)) {
      return null;
    }

    window.sessionStorage.setItem(
      GUEST_RECOMMENDATION_SESSION_STORAGE_KEY,
      generated,
    );
    return generated;
  } catch {
    return null;
  }
};
