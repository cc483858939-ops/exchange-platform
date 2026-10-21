package controllers

import (
	"errors"

	"Go.exchange/apierrors"

	"github.com/gin-gonic/gin"
)

func writeAPIError(ctx *gin.Context, code string) {
	apierrors.WritePublicError(ctx, apierrors.Code(code))
}

func writeInternalAPIError(ctx *gin.Context, err error) {
	apierrors.WriteInternalError(ctx, err)
}

func writeInternalAPIErrorAs(ctx *gin.Context, err error, code string) {
	apierrors.WriteInternalErrorAs(ctx, err, apierrors.Code(code))
}

func writeInternalAPIErrorCode(ctx *gin.Context, code string) {
	writeInternalAPIErrorAs(ctx, errors.New("internal operation unavailable"), code)
}

func logAPIInternalError(ctx *gin.Context, status int, err error) {
	apierrors.LogInternalError(ctx, status, apierrors.CodeInternalError, err)
}
