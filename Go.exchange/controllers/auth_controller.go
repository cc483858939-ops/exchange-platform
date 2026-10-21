package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"Go.exchange/auth"
	"Go.exchange/metrics"
	"Go.exchange/models"
	"Go.exchange/postmention"
	"Go.exchange/utils"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	ginjson "github.com/gin-gonic/gin/codec/json"
	"github.com/jackc/pgx/v5/pgconn"
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
var errRegistrationOnboardingInit = errors.New("initialize registration onboarding state")
var errRegistrationUsernameConflict = errors.New("registration username conflict")

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
			writeAuthError(ctx, "AUTH_REQUEST_TOO_LARGE")
			return
		}
		writeAuthError(ctx, "AUTH_REQUEST_INVALID")
		return
	}
	if !postmention.ValidUsername(request.Username) {
		writeAuthError(ctx, "AUTH_REQUEST_INVALID")
		return
	}
	if !c.allowAttempt(ctx, auth.AttemptRegister, strings.ToLower(strings.TrimSpace(request.Username))) {
		return
	}

	hashedPassword, err := utils.HashPassword(request.Password)
	if err != nil {
		writeInternalAPIErrorAs(ctx, err, "AUTH_INTERNAL")
		return
	}
	user := models.User{Username: request.Username, Password: hashedPassword}
	if c.userLikeInit == nil {
		metrics.RecordLikeLifecycleEvent("user_init_failure")
		writeInternalAPIErrorAs(ctx, errors.New("user like initializer is unavailable"), "AUTH_LIKE_STATE_UNAVAILABLE")
		return
	}
	requestCtx := ctx.Request.Context()
	userLikeInitAttempted := false
	err = c.db.WithContext(requestCtx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			if isRegistrationUniqueViolation(err) {
				return fmt.Errorf("%w: %w", errRegistrationUsernameConflict, err)
			}
			return err
		}
		if err := tx.Create(&models.UserOnboarding{UserID: user.ID, CurrentStep: models.UserOnboardingStepAvatar}).Error; err != nil {
			return fmt.Errorf("%w: %w", errRegistrationOnboardingInit, err)
		}
		userLikeInitAttempted = true
		if err := c.userLikeInit.InitializeUserEmpty(requestCtx, user.ID); err != nil {
			metrics.RecordLikeLifecycleEvent("user_init_failure")
			return fmt.Errorf("%w: %w", errRegistrationUserLikeInit, err)
		}
		return nil
	})
	if err != nil {
		if userLikeInitAttempted {
			c.cleanupRolledBackUserLikeInitialization(ctx, user.ID)
		}
		if errors.Is(err, errRegistrationUserLikeInit) {
			writeInternalAPIErrorAs(ctx, err, "AUTH_LIKE_STATE_UNAVAILABLE")
			return
		}
		if errors.Is(err, errRegistrationOnboardingInit) {
			writeInternalAPIErrorAs(ctx, err, "AUTH_ONBOARDING_INIT_FAILED")
			return
		}
		if errors.Is(err, errRegistrationUsernameConflict) {
			writeAuthError(ctx, "AUTH_USERNAME_UNAVAILABLE")
			return
		}
		writeInternalAPIErrorAs(ctx, err, "AUTH_INTERNAL")
		return
	}

	pair, err := c.tokens.IssuePair(ctx.Request.Context(), user.ID)
	if err != nil {
		writeInternalAPIErrorAs(ctx, err, "AUTH_TOKEN_ISSUE_FAILED")
		return
	}
	writeAuthResponse(ctx, pair, user)
}

func isRegistrationUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (c *AuthController) cleanupRolledBackUserLikeInitialization(request *gin.Context, userID uint) {
	if c == nil || c.db == nil || request == nil || request.Request == nil || userID == 0 {
		return
	}
	requestContext := request.Request.Context()
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(requestContext), time.Second)
	defer cancel()
	var existing models.User
	err := c.db.WithContext(checkCtx).Unscoped().Select("id").Where("id = ?", userID).Take(&existing).Error
	if err == nil {
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		logAPIInternalError(request, http.StatusInternalServerError, err)
		return
	}
	compensator, ok := c.userLikeInit.(userLikeInitCompensator)
	if !ok {
		return
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(requestContext), time.Second)
	defer cleanupCancel()
	if err := compensator.CleanupUncommittedUserLikeInitialization(cleanupCtx, userID); err != nil {
		logAPIInternalError(request, http.StatusInternalServerError, err)
	}
}

func (c *AuthController) Login(ctx *gin.Context) {
	var request loginRequest
	if err := bindAuthJSON(ctx, &request); err != nil {
		if isAuthRequestTooLarge(err) {
			writeAuthError(ctx, "AUTH_REQUEST_TOO_LARGE")
			return
		}
		writeAuthError(ctx, "AUTH_REQUEST_INVALID")
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
			writeAuthError(ctx, "AUTH_CREDENTIALS_INVALID")
			return
		}
		writeInternalAPIErrorAs(ctx, err, "AUTH_INTERNAL")
		return
	}
	if !utils.CheckPassword(request.Password, user.Password) {
		writeAuthError(ctx, "AUTH_CREDENTIALS_INVALID")
		return
	}

	pair, err := c.tokens.IssuePair(ctx.Request.Context(), user.ID)
	if err != nil {
		writeInternalAPIErrorAs(ctx, err, "AUTH_TOKEN_ISSUE_FAILED")
		return
	}
	writeAuthResponse(ctx, pair, user)
}

func (c *AuthController) Refresh(ctx *gin.Context) {
	var request refreshRequest
	if err := bindAuthJSON(ctx, &request); err != nil {
		if isAuthRequestTooLarge(err) {
			writeAuthError(ctx, "AUTH_REQUEST_TOO_LARGE")
			return
		}
		writeAuthError(ctx, "AUTH_REQUEST_INVALID")
		return
	}
	if !c.allowAttempt(ctx, auth.AttemptRefresh, strings.TrimSpace(request.RefreshToken)) {
		return
	}

	pair, err := c.tokens.RotateRefresh(ctx.Request.Context(), request.RefreshToken, request.RequestID)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrRefreshExpired):
			writeAuthError(ctx, "AUTH_REFRESH_EXPIRED")
		case errors.Is(err, auth.ErrRefreshReused):
			writeAuthError(ctx, "AUTH_REFRESH_REUSED")
		case errors.Is(err, auth.ErrRefreshInvalid):
			writeAuthError(ctx, "AUTH_REFRESH_INVALID")
		default:
			writeInternalAPIErrorAs(ctx, err, "AUTH_INTERNAL")
		}
		return
	}

	var user models.User
	if err := c.db.WithContext(ctx.Request.Context()).Select("id", "username", "display_name", "avatar_url").First(&user, pair.UserID).Error; err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAuthError(ctx, "AUTH_REFRESH_INVALID")
		} else {
			writeInternalAPIErrorAs(ctx, err, "AUTH_INTERNAL")
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
		writeInternalAPIErrorAs(ctx, errors.New("auth attempt limiter is unavailable"), "AUTH_RATE_LIMIT_UNAVAILABLE")
		return false
	}
	decision, err := c.limiter.Allow(ctx.Request.Context(), auth.AttemptInput{
		Action:   action,
		ClientIP: ctx.ClientIP(),
		Subject:  subject,
	})
	if err != nil {
		writeInternalAPIErrorAs(ctx, err, "AUTH_RATE_LIMIT_UNAVAILABLE")
		return false
	}
	if decision.Allowed {
		return true
	}
	ctx.Header("Retry-After", strconv.Itoa(auth.RetryAfterSeconds(action, decision.RetryAfter)))
	writeAuthError(ctx, "AUTH_RATE_LIMITED")
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

func writeAuthError(ctx *gin.Context, code string) {
	writeAPIError(ctx, code)
}
