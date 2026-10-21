package controllers

import (
	"errors"
	"net/http"

	"Go.exchange/auth"

	"github.com/gin-gonic/gin"
)

type logoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Logout authenticates using the opaque refresh credential, so an expired
// Access JWT does not prevent single-session logout. It never accepts a sid
// supplied on its own and never issues replacement credentials.
func (c *AuthController) Logout(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	var request logoutRequest
	if err := bindAuthJSON(ctx, &request); err != nil {
		if isAuthRequestTooLarge(err) {
			writeAuthError(ctx, "AUTH_REQUEST_TOO_LARGE")
		} else {
			writeAuthError(ctx, "AUTH_REQUEST_INVALID")
		}
		return
	}
	if !c.allowAttempt(ctx, auth.AttemptLogout, request.RefreshToken) {
		return
	}
	if err := c.tokens.RevokeRefresh(ctx.Request.Context(), request.RefreshToken); err != nil {
		if errors.Is(err, auth.ErrRefreshInvalid) {
			writeAuthError(ctx, "AUTH_REFRESH_INVALID")
		} else {
			writeInternalAPIErrorAs(ctx, err, "AUTH_LOGOUT_UNAVAILABLE")
		}
		return
	}
	ctx.Status(http.StatusNoContent)
}
