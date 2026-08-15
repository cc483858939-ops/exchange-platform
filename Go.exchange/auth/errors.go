package auth

import "errors"

var (
	ErrAccessTokenInvalid = errors.New("access token is invalid")
	ErrRefreshInvalid     = errors.New("refresh token is invalid")
	ErrRefreshExpired     = errors.New("refresh token is expired")
	ErrRefreshReused      = errors.New("refresh token was already rotated")
)
