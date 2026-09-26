package controllers

import (
	"time"

	"Go.exchange/models"
	"gorm.io/gorm"
)

type replyResponse = postResponse

type replyListResponse struct {
	Items      []replyResponse `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

func newReplyResponse(db *gorm.DB, post models.Post) (replyResponse, error) {
	response, err := newPostResponse(post)
	if err != nil {
		return replyResponse{}, err
	}
	if post.ReplyToPostID != nil {
		response.ReplyToPost, err = loadPostReferenceFromDB(db, post.ReplyToPostID, time.Now().UTC())
		if err != nil {
			return replyResponse{}, err
		}
	}
	return response, nil
}
