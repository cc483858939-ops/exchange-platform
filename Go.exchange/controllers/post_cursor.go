package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type postPageResponse struct {
	Items      []postResponse `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

func writePostTimelineStoreError(ctx *gin.Context, err error) {
	if handleRequestDBError(ctx, err) {
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}
