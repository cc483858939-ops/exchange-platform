package controllers

import (
	"errors"
	"math"
	"net/http"
	"strconv"

	"Go.exchange/auth"
	"Go.exchange/models"
	"Go.exchange/utils"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
}

var (
	errCurrentPasswordInvalid = errors.New("current password does not match")
	errPasswordUnchanged      = errors.New("new password matches current password")
	errPasswordUserMissing    = errors.New("password account no longer exists")
)

// ChangePassword changes the authenticated user's password and invalidates
// every access and refresh credential issued under the previous auth_version.
func (c *AuthController) ChangePassword(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	var request changePasswordRequest
	if err := bindAuthJSON(ctx, &request); err != nil {
		if isAuthRequestTooLarge(err) {
			writeAuthError(ctx, "AUTH_REQUEST_TOO_LARGE")
		} else {
			writeAuthError(ctx, "AUTH_REQUEST_INVALID")
		}
		return
	}
	if err := validateNewPasswordLength(request.NewPassword); errors.Is(err, bcrypt.ErrPasswordTooLong) {
		writeAuthError(ctx, "AUTH_PASSWORD_TOO_LONG")
		return
	}
	userID, ok := authenticatedUserID(ctx)
	if !ok {
		writeAuthError(ctx, "AUTH_TOKEN_INVALID")
		return
	}
	if !c.allowAttempt(ctx, auth.AttemptChangePassword, strconv.FormatUint(uint64(userID), 10)) {
		return
	}

	var observed models.User
	if err := c.db.WithContext(ctx.Request.Context()).Select("id", "password", "auth_version").First(&observed, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAuthError(ctx, "AUTH_TOKEN_INVALID")
		} else if handleRequestDBError(ctx, err) {
			return
		} else {
			writeInternalAPIErrorAs(ctx, err, "AUTH_INTERNAL")
		}
		return
	}
	if observed.AuthVersion <= 0 {
		writeInternalAPIErrorAs(ctx, auth.ErrAuthVersionUnavailable, "AUTH_VERSION_UNAVAILABLE")
		return
	}
	if err := verifySubmittedCurrentPassword(request.CurrentPassword, observed.Password); err != nil {
		writePasswordVerificationError(ctx, err)
		return
	}
	if err := ensureNewPasswordDiffers(request.CurrentPassword, request.NewPassword); err != nil {
		writePasswordVerificationError(ctx, err)
		return
	}

	hashPassword := c.hashPassword
	if hashPassword == nil {
		hashPassword = utils.HashPassword
	}
	newHash, err := hashPassword(request.NewPassword)
	if err != nil {
		if errors.Is(err, bcrypt.ErrPasswordTooLong) {
			writeAuthError(ctx, "AUTH_PASSWORD_TOO_LONG")
		} else {
			writeInternalAPIErrorAs(ctx, err, "AUTH_INTERNAL")
		}
		return
	}

	err = c.db.WithContext(ctx.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var latest models.User
		err := tx.Select("id", "password", "auth_version").Clauses(clause.Locking{Strength: "UPDATE"}).First(&latest, userID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errPasswordUserMissing
		}
		if err != nil {
			return err
		}
		if latest.AuthVersion <= 0 || latest.AuthVersion == math.MaxInt64 {
			return auth.ErrAuthVersionUnavailable
		}
		if latest.AuthVersion != observed.AuthVersion || latest.Password != observed.Password {
			if err := verifySubmittedCurrentPassword(request.CurrentPassword, latest.Password); err != nil {
				return err
			}
		}
		if err := ensureNewPasswordDiffers(request.CurrentPassword, request.NewPassword); err != nil {
			return err
		}
		result := tx.Model(&models.User{}).
			Where("id = ? AND auth_version = ?", userID, latest.AuthVersion).
			Updates(map[string]any{"password": newHash, "auth_version": latest.AuthVersion + 1})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errCurrentPasswordInvalid
		}
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errCurrentPasswordInvalid):
			writeAuthError(ctx, "AUTH_CURRENT_PASSWORD_INVALID")
		case errors.Is(err, errPasswordUnchanged):
			writeAuthError(ctx, "AUTH_PASSWORD_UNCHANGED")
		case errors.Is(err, errPasswordUserMissing):
			writeAuthError(ctx, "AUTH_TOKEN_INVALID")
		case errors.Is(err, auth.ErrAuthVersionUnavailable):
			writeInternalAPIErrorAs(ctx, err, "AUTH_VERSION_UNAVAILABLE")
		case handleRequestDBError(ctx, err):
		default:
			writeInternalAPIErrorAs(ctx, err, "AUTH_INTERNAL")
		}
		return
	}
	ctx.Status(http.StatusNoContent)
}

func authenticatedUserID(ctx *gin.Context) (uint, bool) {
	if ctx == nil {
		return 0, false
	}
	value, ok := ctx.Get("user_id")
	if !ok {
		return 0, false
	}
	userID, ok := value.(uint)
	return userID, ok && userID > 0
}

func verifySubmittedCurrentPassword(plain, hashed string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain))
	if err == nil {
		return nil
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) || errors.Is(err, bcrypt.ErrPasswordTooLong) {
		return errCurrentPasswordInvalid
	}
	return err
}

func validateNewPasswordLength(plain string) error {
	if len([]byte(plain)) > maxBcryptPasswordBytes {
		return bcrypt.ErrPasswordTooLong
	}
	return nil
}

func ensureNewPasswordDiffers(current, next string) error {
	if current == next {
		return errPasswordUnchanged
	}
	return nil
}

func writePasswordVerificationError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, errCurrentPasswordInvalid):
		writeAuthError(ctx, "AUTH_CURRENT_PASSWORD_INVALID")
	case errors.Is(err, errPasswordUnchanged):
		writeAuthError(ctx, "AUTH_PASSWORD_UNCHANGED")
	default:
		writeInternalAPIErrorAs(ctx, err, "AUTH_INTERNAL")
	}
}
