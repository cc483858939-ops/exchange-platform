package controllers

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
)

func parsePageLimit(ctx *gin.Context, defaultLimit, maxLimit int) (int, error) {
	limit := defaultLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return limit, nil
}
