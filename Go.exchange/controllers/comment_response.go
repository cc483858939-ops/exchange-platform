package controllers

import (
	"time"

	"Go.exchange/models"
)

type replyResponse = postResponse

type replyListResponse struct {
	Items      []replyResponse `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

func newReplyResponse(post models.Post) (replyResponse, error) {
	response, err := newPostResponse(post)
	if err != nil {
		return replyResponse{}, err
	}
	if post.ReplyToPostID != nil {
		response.ReplyToPost, err = loadPostReference(post.ReplyToPostID, time.Now().UTC())
		if err != nil {
			return replyResponse{}, err
		}
	}
	return response, nil
}
