package controllers

import (
	"Go.exchange/apierrors"

	"github.com/gin-gonic/gin"
)

// handleRequestDBError preserves the existing cancellation and PostgreSQL
// timeout call-site contract while delegating its public response to the
// shared API error catalog.
func handleRequestDBError(ctx *gin.Context, err error) bool {
	return apierrors.HandleRequestError(ctx, err)
}

func isPostgresTimeoutError(err error) bool {
	return apierrors.IsPostgresTimeoutError(err)
}
