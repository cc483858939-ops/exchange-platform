export interface Article {
  ID: number;
  title: string;
  preview: string;
  content: string;
  cover_image_url?: string;
  expired_at?: string;
}

export interface Like {
  likes: number;
  liked: boolean;
}
