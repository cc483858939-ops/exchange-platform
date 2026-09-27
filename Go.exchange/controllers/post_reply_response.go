package controllers

type replyResponse = postResponse

type replyListResponse struct {
	Items      []replyResponse `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}
