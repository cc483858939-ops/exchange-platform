export type AuthRequestBinding = Readonly<{
  userID: number;
  sessionID: string;
  sessionVersion: number;
}>;
