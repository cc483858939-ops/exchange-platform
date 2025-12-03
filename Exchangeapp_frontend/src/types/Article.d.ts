export interface Article {
    ID: string;
    Title: string;
    Preview: string;
    Content: string;
    expired_at?: string; 
}

export interface Like{
    likes: number
}