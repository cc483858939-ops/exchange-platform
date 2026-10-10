package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"Go.exchange/auth"
	"Go.exchange/metrics"
	"Go.exchange/models"
	"Go.exchange/utils"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	ginjson "github.com/gin-gonic/gin/codec/json"
	"gorm.io/gorm"
)

type registerRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
	RequestID    string `json:"request_id"`
}

type authUserResponse struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

type authResponse struct {
	AccessToken      string           `json:"access_token"`
	RefreshToken     string           `json:"refresh_token"`
	TokenType        string           `json:"token_type"`
	ExpiresIn        int64            `json:"expires_in"`
	RefreshExpiresIn int64            `json:"refresh_expires_in"`
	User             authUserResponse `json:"user"`
}

type AuthController struct {
	db           *gorm.DB
	tokens       auth.TokenService
	limiter      auth.AttemptLimiter
	userLikeInit interface {
		InitializeUserEmpty(context.Context, uint) error
	}
}

type userLikeInitCompensator interface {
	CleanupUncommittedUserLikeInitialization(context.Context, uint) error
}

var errRegistrationUserLikeInit = errors.New("initialize registration user like state")

const authRequestMaxBodyBytes int64 = 16 << 10

func NewAuthController(db *gorm.DB, tokens auth.TokenService, limiter auth.AttemptLimiter, userLikeInitializers ...interface {
	InitializeUserEmpty(context.Context, uint) error
}) (*AuthController, error) {
	if db == nil {
		return nil, errors.New("auth database is required")
	}
	if tokens == nil {
		return nil, errors.New("auth token service is required")
	}
	if limiter == nil {
		return nil, errors.New("auth attempt limiter is required")
	}
	controller := &AuthController{db: db, tokens: tokens, limiter: limiter}
	if len(userLikeInitializers) > 0 {
		controller.userLikeInit = userLikeInitializers[0]
	}
	return controller, nil
}

func (c *AuthController) Register(ctx *gin.Context) {
	var request registerRequest
	if err := bindAuthJSON(ctx, &request); err != nil {
		if isAuthRequestTooLarge(err) {
			writeAuthError(ctx, http.StatusRequestEntityTooLarge, "AUTH_REQUEST_TOO_LARGE", "Authentication request is too large")
			return
		}
		writeAuthError(ctx, http.StatusBadRequest, "AUTH_REQUEST_INVALID", "Invalid request data")
		return
	}
	if !c.allowAttempt(ctx, auth.AttemptRegister, strings.ToLower(strings.TrimSpace(request.Username))) {
		return
	}

	hashedPassword, err := utils.HashPassword(request.Password)
	if err != nil {
		writeAuthError(ctx, http.StatusInternalServerError, "AUTH_INTERNAL", "Authentication failed")
		return
	}
	user := models.User{Username: request.Username, Password: hashedPassword}
	if c.userLikeInit == nil {
		metrics.RecordLikeLifecycleEvent("user_init_failure")
		writeAuthError(ctx, http.StatusServiceUnavailable, "AUTH_LIKE_STATE_UNAVAILABLE", "Registration is temporarily unavailable")
		return
	}
	requestCtx := ctx.Request.Context()
	userLikeInitAttempted := false
	err = c.db.WithContext(requestCtx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		userLikeInitAttempted = true
		if err := c.userLikeInit.InitializeUserEmpty(requestCtx, user.ID); err != nil {
			metrics.RecordLikeLifecycleEvent("user_init_failure")
			log.Printf("[AuthRegister] initialize User Like state user=%d: %v", user.ID, err)
			return fmt.Errorf("%w: %v", errRegistrationUserLikeInit, err)
		}
		return nil
	})
	if err != nil {
		if userLikeInitAttempted {
			c.cleanupRolledBackUserLikeInitialization(requestCtx, user.ID)
		}
		if errors.Is(err, errRegistrationUserLikeInit) {
			writeAuthError(ctx, http.StatusServiceUnavailable, "AUTH_LIKE_STATE_UNAVAILABLE", "Registration is temporarily unavailable")
			return
		}
		if handleRequestDBError(ctx, err) {
			return
		}
		writeAuthError(ctx, http.StatusConflict, "AUTH_USERNAME_UNAVAILABLE", "Username is unavailable")
		return
	}

	pair, err := c.tokens.IssuePair(ctx.Request.Context(), user.ID)
	if err != nil {
		writeAuthError(ctx, http.StatusInternalServerError, "AUTH_TOKEN_ISSUE_FAILED", "Authentication failed")
		return
	}
	writeAuthResponse(ctx, pair, user)
}

func (c *AuthController) cleanupRolledBackUserLikeInitialization(ctx context.Context, userID uint) {
	if c == nil || c.db == nil || userID == 0 {
		return
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	var existing models.User
	err := c.db.WithContext(checkCtx).Unscoped().Select("id").Where("id = ?", userID).Take(&existing).Error
	if err == nil {
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("[AuthRegister] preserve User Like initialization user=%d because rollback status is unknown: %v", userID, err)
		return
	}
	compensator, ok := c.userLikeInit.(userLikeInitCompensator)
	if !ok {
		return
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cleanupCancel()
	if err := compensator.CleanupUncommittedUserLikeInitialization(cleanupCtx, userID); err != nil {
		log.Printf("[AuthRegister] cleanup rolled-back User Like initialization user=%d: %v", userID, err)
	}
}

func (c *AuthController) Login(ctx *gin.Context) {
	var request loginRequest
	if err := bindAuthJSON(ctx, &request); err != nil {
		if isAuthRequestTooLarge(err) {
			writeAuthError(ctx, http.StatusRequestEntityTooLarge, "AUTH_REQUEST_TOO_LARGE", "Authentication request is too large")
			return
		}
		writeAuthError(ctx, http.StatusBadRequest, "AUTH_REQUEST_INVALID", "Invalid request data")
		return
	}
	if !c.allowAttempt(ctx, auth.AttemptLogin, strings.ToLower(strings.TrimSpace(request.Username))) {
		return
	}

	var user models.User
	if err := c.db.WithContext(ctx.Request.Context()).Where("username = ?", request.Username).First(&user).Error; err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAuthError(ctx, http.StatusUnauthorized, "AUTH_CREDENTIALS_INVALID", "Invalid username or password")
			return
		}
		writeAuthError(ctx, http.StatusInternalServerError, "AUTH_INTERNAL", "Authentication failed")
		return
	}
	if !utils.CheckPassword(request.Password, user.Password) {
		writeAuthError(ctx, http.StatusUnauthorized, "AUTH_CREDENTIALS_INVALID", "Invalid username or password")
		return
	}

	pair, err := c.tokens.IssuePair(ctx.Request.Context(), user.ID)
	if err != nil {
		writeAuthError(ctx, http.StatusInternalServerError, "AUTH_TOKEN_ISSUE_FAILED", "Authentication failed")
		return
	}
	writeAuthResponse(ctx, pair, user)
}

func (c *AuthController) Refresh(ctx *gin.Context) {
	var request refreshRequest
	if err := bindAuthJSON(ctx, &request); err != nil {
		if isAuthRequestTooLarge(err) {
			writeAuthError(ctx, http.StatusRequestEntityTooLarge, "AUTH_REQUEST_TOO_LARGE", "Authentication request is too large")
			return
		}
		writeAuthError(ctx, http.StatusBadRequest, "AUTH_REQUEST_INVALID", "Invalid request data")
		return
	}
	if !c.allowAttempt(ctx, auth.AttemptRefresh, strings.TrimSpace(request.RefreshToken)) {
		return
	}

	pair, err := c.tokens.RotateRefresh(ctx.Request.Context(), request.RefreshToken, request.RequestID)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrRefreshExpired):
			writeAuthError(ctx, http.StatusUnauthorized, "AUTH_REFRESH_EXPIRED", "Authentication failed")
		case errors.Is(err, auth.ErrRefreshReused):
			writeAuthError(ctx, http.StatusUnauthorized, "AUTH_REFRESH_REUSED", "Authentication failed")
		case errors.Is(err, auth.ErrRefreshInvalid):
			writeAuthError(ctx, http.StatusUnauthorized, "AUTH_REFRESH_INVALID", "Authentication failed")
		default:
			writeAuthError(ctx, http.StatusInternalServerError, "AUTH_INTERNAL", "Authentication failed")
		}
		return
	}

	var user models.User
	if err := c.db.WithContext(ctx.Request.Context()).Select("id", "username", "display_name", "avatar_url").First(&user, pair.UserID).Error; err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAuthError(ctx, http.StatusUnauthorized, "AUTH_REFRESH_INVALID", "Authentication failed")
		} else {
			writeAuthError(ctx, http.StatusInternalServerError, "AUTH_INTERNAL", "Authentication failed")
		}
		return
	}
	writeAuthResponse(ctx, pair, user)
}

func bindAuthJSON(ctx *gin.Context, destination any) error {
	if ctx == nil || ctx.Request == nil || ctx.Request.Body == nil {
		return errors.New("authentication request body is unavailable")
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, authRequestMaxBodyBytes)
	decoder := ginjson.API.NewDecoder(ctx.Request.Body)
	if binding.EnableDecoderUseNumber {
		decoder.UseNumber()
	}
	if binding.EnableDecoderDisallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if binding.Validator != nil {
		if err := binding.Validator.ValidateStruct(destination); err != nil {
			return err
		}
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values in authentication request")
		}
		return err
	}
	return nil
}

func isAuthRequestTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}

func (c *AuthController) allowAttempt(ctx *gin.Context, action auth.AttemptAction, subject string) bool {
	if c == nil || c.limiter == nil {
		writeAuthError(ctx, http.StatusServiceUnavailable, "AUTH_RATE_LIMIT_UNAVAILABLE", "Authentication temporarily unavailable")
		return false
	}
	decision, err := c.limiter.Allow(ctx.Request.Context(), auth.AttemptInput{
		Action:   action,
		ClientIP: ctx.ClientIP(),
		Subject:  subject,
	})
	if err != nil {
		writeAuthError(ctx, http.StatusServiceUnavailable, "AUTH_RATE_LIMIT_UNAVAILABLE", "Authentication temporarily unavailable")
		return false
	}
	if decision.Allowed {
		return true
	}
	ctx.Header("Retry-After", strconv.Itoa(auth.RetryAfterSeconds(action, decision.RetryAfter)))
	writeAuthError(ctx, http.StatusTooManyRequests, "AUTH_RATE_LIMITED", "Too many authentication attempts")
	return false
}

func writeAuthResponse(ctx *gin.Context, pair auth.TokenPair, user models.User) {
	ctx.JSON(http.StatusOK, authResponse{
		AccessToken:      pair.AccessToken,
		RefreshToken:     pair.RefreshToken,
		TokenType:        pair.TokenType,
		ExpiresIn:        int64(pair.AccessExpiresIn.Seconds()),
		RefreshExpiresIn: int64(pair.RefreshExpiresIn.Seconds()),
		User: authUserResponse{
			ID:          user.ID,
			Username:    user.Username,
			DisplayName: user.DisplayName,
			AvatarURL:   user.AvatarURL,
		},
	})
}

func writeAuthError(ctx *gin.Context, status int, code string, message string) {
	ctx.JSON(status, gin.H{"code": code, "message": message})
}
