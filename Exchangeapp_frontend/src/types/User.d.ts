export interface PublicAuthor {
  id: number;
  username: string;
  display_name: string;
  avatar_url: string;
}

export interface PublicUser extends PublicAuthor {
  bio: string;
  created_at: string;
}

