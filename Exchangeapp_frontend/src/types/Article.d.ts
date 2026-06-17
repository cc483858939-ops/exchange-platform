export interface Article {
    ID: number;
    title: string;
    preview: string;
    content: string;
    expired_at?: string;
}

export interface Like{
    likes: number
    liked: boolean
}
