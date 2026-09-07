import apiClient from '../axios';
export async function getLikedHistory(query = {}) {
    const response = await apiClient.get('/me/history/likes', { params: query });
    return response.data;
}
