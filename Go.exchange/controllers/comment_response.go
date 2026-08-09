package controllers

import (
	"errors"
	"time"

	"Go.exchange/models"
)

type commentResponse struct {
	ID        uint                 `json:"id"`
	ArticleID uint                 `json:"article_id"`
	Content   string               `json:"content"`
	CreatedAt time.Time            `json:"created_at"`
	Author    publicAuthorResponse `json:"author"`
}

type commentListResponse struct {
	Items      []commentResponse `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

func publicAuthorFromComment(comment models.Comment) (publicAuthorResponse, error) {
	if comment.UserID == 0 || comment.Author.ID == 0 || comment.Author.ID != comment.UserID {
		return publicAuthorResponse{}, errors.New("comment author is missing or invalid")
	}
	return publicAuthorResponse{ID: comment.Author.ID, Username: comment.Author.Username}, nil
}

func newCommentResponse(comment models.Comment) (commentResponse, error) {
	author, err := publicAuthorFromComment(comment)
	if err != nil {
		return commentResponse{}, err
	}
	return commentResponse{
		ID:        comment.ID,
		ArticleID: comment.ArticleID,
		Content:   comment.Content,
		CreatedAt: comment.CreatedAt,
		Author:    author,
	}, nil
}
