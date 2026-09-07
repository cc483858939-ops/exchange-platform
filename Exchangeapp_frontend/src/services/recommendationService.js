import apiClient from '../axios';
export async function getPostRecommendations(limit) {
    if (limit !== undefined && (!Number.isSafeInteger(limit) || limit <= 0)) {
        throw new Error('Invalid recommendation limit');
    }
    const response = await apiClient.get('/recommendations/posts', {
        params: limit === undefined ? undefined : { limit },
    });
    return response.data;
}
