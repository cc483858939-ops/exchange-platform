package controllers

import "github.com/gin-gonic/gin"

type postPageResponse struct {
	Items      []postResponse `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

func writePostTimelineStoreError(ctx *gin.Context, err error) {
	writeInternalAPIError(ctx, err)
}
