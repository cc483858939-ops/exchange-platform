export interface PublicAuthor {
  id: number;
  username: string;
  display_name: string;
  avatar_url: string;
}

export interface PublicUserSummary extends PublicAuthor {
  bio: string;
  created_at: string;
}

export interface PublicUser extends PublicUserSummary {
  cover_image_url: string;
  follower_count: number;
  following_count: number;
}

