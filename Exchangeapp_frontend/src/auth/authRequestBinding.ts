export type AuthRequestBinding = Readonly<{
  userID: number;
  sessionID: string;
  sessionVersion: number;
}>;

export const matchesDurableAuthOwner = (
  binding: AuthRequestBinding | null,
  ownerUserID: number,
  ownerSessionID: string | null | undefined,
): boolean => Boolean(
  binding
  && typeof ownerSessionID === 'string'
  && ownerSessionID.trim()
  && binding.userID === ownerUserID
  && binding.sessionID === ownerSessionID
);
