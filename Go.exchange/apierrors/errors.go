package apierrors

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

const RequestIDHeader = "X-Request-ID"

type Code string

const (
	CodeInvalidRequest               Code = "INVALID_REQUEST"
	CodeUnauthorized                 Code = "UNAUTHORIZED"
	CodeForbidden                    Code = "FORBIDDEN"
	CodeNotFound                     Code = "NOT_FOUND"
	CodeMethodNotAllowed             Code = "METHOD_NOT_ALLOWED"
	CodeConflict                     Code = "CONFLICT"
	CodePayloadTooLarge              Code = "PAYLOAD_TOO_LARGE"
	CodeValidationFailed             Code = "VALIDATION_FAILED"
	CodeRateLimited                  Code = "RATE_LIMITED"
	CodeInternalError                Code = "INTERNAL_ERROR"
	CodeServiceUnavailable           Code = "SERVICE_UNAVAILABLE"
	CodeRequestTimeout               Code = "REQUEST_TIMEOUT"
	CodeInvalidCursor                Code = "INVALID_CURSOR"
	CodeCursorExpired                Code = "CURSOR_EXPIRED"
	CodePostNotFound                 Code = "POST_NOT_FOUND"
	CodeUserNotFound                 Code = "USER_NOT_FOUND"
	CodeTopicNotFound                Code = "TOPIC_NOT_FOUND"
	CodeNotificationNotFound         Code = "NOTIFICATION_NOT_FOUND"
	CodeFileNotFound                 Code = "FILE_NOT_FOUND"
	CodePostReferenceUnavailable     Code = "POST_REFERENCE_UNAVAILABLE"
	CodePostIdempotencyConflict      Code = "POST_IDEMPOTENCY_CONFLICT"
	CodePostMentionLimitExceeded     Code = "POST_MENTION_LIMIT_EXCEEDED"
	CodeContentIsRequired            Code = "CONTENT_IS_REQUIRED"
	CodeReplyContentTooLong          Code = "REPLY_CONTENT_EXCEEDS_1000_UNICODE_CODE_POINTS"
	CodeInvalidReplyToPostID         Code = "INVALID_REPLY_TO_POST_ID"
	CodeInvalidQuotePostID           Code = "INVALID_QUOTE_POST_ID"
	CodeReplyAndQuoteConflict        Code = "A_POST_CANNOT_BE_BOTH_A_REPLY_AND_A_QUOTE"
	CodeTooManyMentionCandidates     Code = "TOO_MANY_MENTION_CANDIDATES"
	CodeInvalidMentionText           Code = "INVALID_MENTION_TEXT"
	CodeInvalidMedia                 Code = "INVALID_MEDIA"
	CodeAuthTokenMissing             Code = "AUTH_TOKEN_MISSING"
	CodeAuthTokenInvalid             Code = "AUTH_TOKEN_INVALID"
	CodeAuthCredentialsInvalid       Code = "AUTH_CREDENTIALS_INVALID"
	CodeAuthRequestInvalid           Code = "AUTH_REQUEST_INVALID"
	CodeAuthPasswordTooLong          Code = "AUTH_PASSWORD_TOO_LONG"
	CodeAuthRequestTooLarge          Code = "AUTH_REQUEST_TOO_LARGE"
	CodeAuthInternal                 Code = "AUTH_INTERNAL"
	CodeAuthLikeStateUnavailable     Code = "AUTH_LIKE_STATE_UNAVAILABLE"
	CodeAuthOnboardingInitFailed     Code = "AUTH_ONBOARDING_INIT_FAILED"
	CodeAuthUsernameUnavailable      Code = "AUTH_USERNAME_UNAVAILABLE"
	CodeAuthTokenIssueFailed         Code = "AUTH_TOKEN_ISSUE_FAILED"
	CodeAuthRefreshExpired           Code = "AUTH_REFRESH_EXPIRED"
	CodeAuthRefreshReused            Code = "AUTH_REFRESH_REUSED"
	CodeAuthRefreshInvalid           Code = "AUTH_REFRESH_INVALID"
	CodeAuthRateLimitUnavailable     Code = "AUTH_RATE_LIMIT_UNAVAILABLE"
	CodeAuthRateLimited              Code = "AUTH_RATE_LIMITED"
	CodeAuthLogoutUnavailable        Code = "AUTH_LOGOUT_UNAVAILABLE"
	CodeAuthCurrentPasswordInvalid   Code = "AUTH_CURRENT_PASSWORD_INVALID"
	CodeAuthPasswordUnchanged        Code = "AUTH_PASSWORD_UNCHANGED"
	CodeAuthVersionUnavailable       Code = "AUTH_VERSION_UNAVAILABLE"
	CodeRateLimitUnavailable         Code = "RATE_LIMIT_UNAVAILABLE"
	CodeAuthRequired                 Code = "AUTH_REQUIRED"
	CodeOnboardingStateMissing       Code = "ONBOARDING_STATE_MISSING"
	CodeOnboardingUnavailable        Code = "ONBOARDING_UNAVAILABLE"
	CodeOnboardingTopicsUnavailable  Code = "ONBOARDING_TOPICS_UNAVAILABLE"
	CodeExchangeInvalidCurrency      Code = "INVALID_CURRENCY"
	CodeExchangeInvalidAmount        Code = "INVALID_AMOUNT"
	CodeExchangeUnsupportedCurrency  Code = "UNSUPPORTED_CURRENCY"
	CodeExchangeUnavailable          Code = "EXCHANGE_UNAVAILABLE"
	CodeImageProcessingBusy          Code = "IMAGE_PROCESSING_BUSY"
	CodeRecommendationRetryBatch     Code = "RECOMMENDATION_RETRY_BATCH"
	CodeRecommendationDisabled       Code = "RECOMMENDATION_TELEMETRY_DISABLED"
	CodePostViewRateLimited          Code = "POST_VIEW_RATE_LIMITED"
	CodeRecommendationRateLimited    Code = "RECOMMENDATION_RATE_LIMITED"
	CodeTranslationRateLimited       Code = "TRANSLATION_RATE_LIMITED"
	CodeUnsupportedImageType         Code = "UNSUPPORTED_IMAGE_TYPE"
	CodeOnboardingAuthRequired       Code = "ONBOARDING_AUTH_REQUIRED"
	CodeOnboardingRequestInvalid     Code = "ONBOARDING_REQUEST_INVALID"
	CodeOnboardingRequestTooLarge    Code = "ONBOARDING_REQUEST_TOO_LARGE"
	CodeOnboardingStepInvalid        Code = "ONBOARDING_STEP_INVALID"
	CodeOnboardingInterestsRequired  Code = "ONBOARDING_INTERESTS_REQUIRED"
	CodeOnboardingInterestsInvalid   Code = "ONBOARDING_INTERESTS_INVALID"
	CodeOnboardingInterestsDuplicate Code = "ONBOARDING_INTERESTS_DUPLICATE"
	CodePostInvalidIdempotencyKey    Code = "POST_INVALID_IDEMPOTENCY_KEY"
)

type Definition struct {
	Status   int
	Message  string
	internal bool
}

type ErrorResponse struct {
	Code      Code   `json:"code"`
	Error     string `json:"error"`
	RequestID string `json:"request_id"`
}

var definitions = map[Code]Definition{
	CodeInvalidRequest:               {Status: http.StatusBadRequest, Message: "Invalid request"},
	CodeContentIsRequired:            {Status: http.StatusBadRequest, Message: "content is required"},
	CodeReplyContentTooLong:          {Status: http.StatusBadRequest, Message: "reply content exceeds 1000 Unicode code points"},
	CodeInvalidReplyToPostID:         {Status: http.StatusBadRequest, Message: "invalid reply_to_post_id"},
	CodeInvalidQuotePostID:           {Status: http.StatusBadRequest, Message: "invalid quote_post_id"},
	CodeReplyAndQuoteConflict:        {Status: http.StatusBadRequest, Message: "a post cannot be both a reply and a quote"},
	CodeTooManyMentionCandidates:     {Status: http.StatusBadRequest, Message: "too many mention candidates"},
	CodeInvalidMentionText:           {Status: http.StatusBadRequest, Message: "invalid mention text"},
	CodeInvalidMedia:                 {Status: http.StatusBadRequest, Message: "invalid media"},
	CodeUnauthorized:                 {Status: http.StatusUnauthorized, Message: "Unauthorized"},
	CodeForbidden:                    {Status: http.StatusForbidden, Message: "Forbidden"},
	CodeNotFound:                     {Status: http.StatusNotFound, Message: "Resource not found"},
	CodeMethodNotAllowed:             {Status: http.StatusMethodNotAllowed, Message: "Method not allowed"},
	CodeConflict:                     {Status: http.StatusConflict, Message: "Request conflict"},
	CodePayloadTooLarge:              {Status: http.StatusRequestEntityTooLarge, Message: "Request body too large"},
	CodeValidationFailed:             {Status: http.StatusUnprocessableEntity, Message: "Validation failed"},
	CodeRateLimited:                  {Status: http.StatusTooManyRequests, Message: "Too many requests"},
	CodeInternalError:                {Status: http.StatusInternalServerError, Message: "Internal server error", internal: true},
	CodeServiceUnavailable:           {Status: http.StatusServiceUnavailable, Message: "Service unavailable", internal: true},
	CodeRequestTimeout:               {Status: http.StatusGatewayTimeout, Message: "Request timed out"},
	CodeInvalidCursor:                {Status: http.StatusBadRequest, Message: "Invalid cursor"},
	CodeCursorExpired:                {Status: http.StatusGone, Message: "Cursor expired"},
	CodePostNotFound:                 {Status: http.StatusNotFound, Message: "Post not found"},
	CodeUserNotFound:                 {Status: http.StatusNotFound, Message: "User not found"},
	CodeTopicNotFound:                {Status: http.StatusNotFound, Message: "Topic not found"},
	CodeNotificationNotFound:         {Status: http.StatusNotFound, Message: "Notification not found"},
	CodeFileNotFound:                 {Status: http.StatusNotFound, Message: "File not found"},
	CodePostReferenceUnavailable:     {Status: http.StatusNotFound, Message: "Reply or quote target unavailable"},
	CodePostIdempotencyConflict:      {Status: http.StatusConflict, Message: "Idempotency-Key was already used with a different post payload."},
	CodePostMentionLimitExceeded:     {Status: http.StatusBadRequest, Message: "A post can mention at most 5 different users"},
	CodeAuthTokenMissing:             {Status: http.StatusUnauthorized, Message: "Authentication required"},
	CodeAuthTokenInvalid:             {Status: http.StatusUnauthorized, Message: "Authentication failed"},
	CodeAuthCredentialsInvalid:       {Status: http.StatusUnauthorized, Message: "Invalid username or password"},
	CodeAuthRequestInvalid:           {Status: http.StatusBadRequest, Message: "Invalid request data"},
	CodeAuthPasswordTooLong:          {Status: http.StatusBadRequest, Message: "Password must be 72 bytes or fewer."},
	CodeAuthRequestTooLarge:          {Status: http.StatusRequestEntityTooLarge, Message: "Authentication request is too large"},
	CodeAuthInternal:                 {Status: http.StatusInternalServerError, Message: "Authentication failed", internal: true},
	CodeAuthLikeStateUnavailable:     {Status: http.StatusServiceUnavailable, Message: "Registration is temporarily unavailable", internal: true},
	CodeAuthOnboardingInitFailed:     {Status: http.StatusInternalServerError, Message: "Registration could not be completed", internal: true},
	CodeAuthUsernameUnavailable:      {Status: http.StatusConflict, Message: "Username is unavailable"},
	CodeAuthTokenIssueFailed:         {Status: http.StatusInternalServerError, Message: "Authentication failed", internal: true},
	CodeAuthRefreshExpired:           {Status: http.StatusUnauthorized, Message: "Authentication failed"},
	CodeAuthRefreshReused:            {Status: http.StatusUnauthorized, Message: "Authentication failed"},
	CodeAuthRefreshInvalid:           {Status: http.StatusUnauthorized, Message: "Authentication failed"},
	CodeAuthRateLimitUnavailable:     {Status: http.StatusServiceUnavailable, Message: "Authentication temporarily unavailable", internal: true},
	CodeAuthRateLimited:              {Status: http.StatusTooManyRequests, Message: "Too many authentication attempts"},
	CodeAuthLogoutUnavailable:        {Status: http.StatusServiceUnavailable, Message: "Server sign-out could not be confirmed", internal: true},
	CodeAuthCurrentPasswordInvalid:   {Status: http.StatusBadRequest, Message: "Current password is incorrect"},
	CodeAuthPasswordUnchanged:        {Status: http.StatusBadRequest, Message: "New password must be different"},
	CodeAuthVersionUnavailable:       {Status: http.StatusServiceUnavailable, Message: "Authentication is temporarily unavailable", internal: true},
	CodeRateLimitUnavailable:         {Status: http.StatusServiceUnavailable, Message: "Rate limiting is temporarily unavailable", internal: true},
	CodeAuthRequired:                 {Status: http.StatusUnauthorized, Message: "Authentication required"},
	CodeOnboardingStateMissing:       {Status: http.StatusInternalServerError, Message: "Onboarding state is unavailable", internal: true},
	CodeOnboardingUnavailable:        {Status: http.StatusInternalServerError, Message: "Onboarding is temporarily unavailable", internal: true},
	CodeOnboardingTopicsUnavailable:  {Status: http.StatusInternalServerError, Message: "Onboarding topics are unavailable", internal: true},
	CodeOnboardingAuthRequired:       {Status: http.StatusUnauthorized, Message: "Authentication is required"},
	CodeOnboardingRequestInvalid:     {Status: http.StatusBadRequest, Message: "Invalid onboarding request"},
	CodeOnboardingRequestTooLarge:    {Status: http.StatusRequestEntityTooLarge, Message: "Onboarding request is too large"},
	CodeOnboardingStepInvalid:        {Status: http.StatusBadRequest, Message: "Invalid onboarding step"},
	CodeOnboardingInterestsRequired:  {Status: http.StatusBadRequest, Message: "topic_slugs is required"},
	CodeOnboardingInterestsInvalid:   {Status: http.StatusBadRequest, Message: "Invalid onboarding interests"},
	CodeOnboardingInterestsDuplicate: {Status: http.StatusBadRequest, Message: "Duplicate onboarding interest"},
	CodePostInvalidIdempotencyKey:    {Status: http.StatusBadRequest, Message: "invalid Idempotency-Key"},
	CodeExchangeInvalidCurrency:      {Status: http.StatusBadRequest, Message: "Invalid currency."},
	CodeExchangeInvalidAmount:        {Status: http.StatusBadRequest, Message: "Invalid amount."},
	CodeExchangeUnsupportedCurrency:  {Status: http.StatusUnprocessableEntity, Message: "Currency is not supported."},
	CodeExchangeUnavailable:          {Status: http.StatusServiceUnavailable, Message: "Exchange-rate service is temporarily unavailable.", internal: true},
	CodeImageProcessingBusy:          {Status: http.StatusServiceUnavailable, Message: "Image processing is busy; try again later"},
	CodeRecommendationRetryBatch:     {Status: http.StatusServiceUnavailable, Message: "Recommendation event publication is pending; retry the batch"},
	CodeRecommendationDisabled:       {Status: http.StatusNotFound, Message: "Recommendation telemetry is disabled"},
	CodePostViewRateLimited:          {Status: http.StatusTooManyRequests, Message: "Post view telemetry rate limit exceeded"},
	CodeRecommendationRateLimited:    {Status: http.StatusTooManyRequests, Message: "Recommendation telemetry rate limit exceeded"},
	CodeTranslationRateLimited:       {Status: http.StatusTooManyRequests, Message: "Translation provider rate limit reached"},
	CodeUnsupportedImageType:         {Status: http.StatusBadRequest, Message: "Only JPEG, PNG, or WebP images are supported"},
}

const requestIDContextKey = "api_request_id"

type requestMetadataContextKey struct{}

type requestMetadata struct {
	requestID string
	method    string
	route     string
}

var fallbackIDCounter atomic.Uint64

func RequestIDMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx == nil || ctx.Writer == nil {
			return
		}
		id := newRequestID()
		ctx.Set(requestIDContextKey, id)
		ctx.Writer.Header().Set(RequestIDHeader, id)
		if ctx.Request != nil {
			method := ctx.Request.Method
			if method == "" {
				method = "unknown"
			}
			route := ctx.FullPath()
			if route == "" {
				route = "unmatched"
			}
			requestContext := context.WithValue(ctx.Request.Context(), requestMetadataContextKey{}, requestMetadata{
				requestID: id, method: method, route: route,
			})
			ctx.Request = ctx.Request.WithContext(requestContext)
		}
		ctx.Next()
	}
}

func RequestID(ctx *gin.Context) string {
	if ctx == nil {
		return ""
	}
	if value, exists := ctx.Get(requestIDContextKey); exists {
		if id, ok := value.(string); ok && id != "" {
			return id
		}
	}
	return ensureRequestID(ctx)
}

func PublicErrorResponse(ctx *gin.Context, code Code) (int, ErrorResponse) {
	definition, ok := definitions[code]
	if !ok {
		code = CodeInternalError
		definition = definitions[CodeInternalError]
	}
	return definition.Status, ErrorResponse{
		Code: code, Error: definition.Message, RequestID: ensureRequestID(ctx),
	}
}

func WritePublicError(ctx *gin.Context, code Code) {
	if ctx == nil || ctx.Writer == nil {
		return
	}
	if ctx.Writer.Written() {
		ctx.Abort()
		return
	}
	status, response := PublicErrorResponse(ctx, code)
	ctx.AbortWithStatusJSON(status, response)
}

func WriteInternalError(ctx *gin.Context, err error) {
	WriteInternalErrorAs(ctx, err, CodeInternalError)
}

// LogInternalError records a bounded diagnostic without exposing error text.
// It is used by fail-open paths that continue the request instead of writing
// an error response.
func LogInternalError(ctx *gin.Context, status int, publicCode Code, err error) {
	if err == nil {
		return
	}
	if _, ok := definitions[publicCode]; !ok {
		publicCode = CodeInternalError
	}
	logInternalFailure(ctx, status, string(publicCode), errorCategory(err))
}

func LogInternalContextError(ctx context.Context, status int, publicCode Code, err error) {
	if err == nil {
		return
	}
	if _, ok := definitions[publicCode]; !ok {
		publicCode = CodeInternalError
	}
	var metadata requestMetadata
	if ctx != nil {
		metadata, _ = ctx.Value(requestMetadataContextKey{}).(requestMetadata)
	}
	if metadata.method == "" {
		metadata.method = "unknown"
	}
	if metadata.route == "" {
		metadata.route = "unmatched"
	}
	slog.Default().Error("HTTP request failure",
		"request_id", metadata.requestID,
		"method", metadata.method,
		"route", metadata.route,
		"status_code", status,
		"public_error_code", string(publicCode),
		"internal_error_category", errorCategory(err),
		"service_operation", metadata.route,
	)
}

// WriteInternalErrorAs is for internal failures with a reviewed, allow-listed
// public status such as a known dependency outage. The cause is never returned
// or included in logs.
func WriteInternalErrorAs(ctx *gin.Context, err error, publicCode Code) {
	if err == nil || ctx == nil || ctx.Writer == nil {
		return
	}
	if HandleRequestError(ctx, err) {
		return
	}
	definition, ok := definitions[publicCode]
	if !ok || !definition.internal {
		publicCode = CodeInternalError
		definition = definitions[CodeInternalError]
	}
	if ctx.Writer.Written() {
		logInternalFailure(ctx, ctx.Writer.Status(), string(publicCode), errorCategory(err))
		ctx.Abort()
		return
	}
	logInternalFailure(ctx, definition.Status, string(publicCode), errorCategory(err))
	WritePublicError(ctx, publicCode)
}

// HandleRequestError preserves client disconnect and request/database timeout
// behavior for existing controller call sites.
func HandleRequestError(ctx *gin.Context, err error) bool {
	if ctx == nil || ctx.Request == nil || ctx.Writer == nil {
		return false
	}
	requestErr := ctx.Request.Context().Err()
	if errors.Is(requestErr, context.Canceled) {
		logInternalFailure(ctx, 499, "", "request_cancelled")
		ctx.Abort()
		return true
	}
	if errors.Is(requestErr, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		isPostgresTimeoutError(err) {
		if ctx.Writer.Written() {
			logInternalFailure(ctx, ctx.Writer.Status(), string(CodeRequestTimeout), "request_timeout")
			ctx.Abort()
		} else {
			logInternalFailure(ctx, http.StatusGatewayTimeout, string(CodeRequestTimeout), "request_timeout")
			WritePublicError(ctx, CodeRequestTimeout)
		}
		return true
	}
	return false
}

func Recovery() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		defer func() {
			if recover() == nil {
				return
			}
			if ctx == nil || ctx.Writer == nil {
				return
			}
			if ctx.Writer.Written() {
				logInternalFailure(ctx, ctx.Writer.Status(), string(CodeInternalError), "panic_after_response_started")
				ctx.Abort()
				return
			}
			logInternalFailure(ctx, http.StatusInternalServerError, string(CodeInternalError), "panic")
			if ctx.Request != nil && isJSONAPIPath(ctx.Request.URL.Path) {
				WritePublicError(ctx, CodeInternalError)
				return
			}
			ctx.AbortWithStatus(http.StatusInternalServerError)
		}()
		ctx.Next()
	}
}

func isJSONAPIPath(path string) bool {
	if !strings.HasPrefix(path, "/api/") && path != "/api" {
		return false
	}
	return path != "/api/files" && !strings.HasPrefix(path, "/api/files/")
}

func isPostgresTimeoutError(err error) bool {
	var stateError interface{ SQLState() string }
	if !errors.As(err, &stateError) {
		return false
	}
	switch stateError.SQLState() {
	case "57014", "55P03":
		return true
	default:
		return false
	}
}

func IsPostgresTimeoutError(err error) bool {
	return isPostgresTimeoutError(err)
}

func errorCategory(err error) string {
	if errors.Is(err, context.Canceled) {
		return "request_cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) || isPostgresTimeoutError(err) {
		return "request_timeout"
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return "network_error"
	}
	var sqlState interface{ SQLState() string }
	if errors.As(err, &sqlState) {
		return "database_error"
	}
	return "internal_error"
}

func ensureRequestID(ctx *gin.Context) string {
	if ctx == nil || ctx.Writer == nil {
		return ""
	}
	if value, exists := ctx.Get(requestIDContextKey); exists {
		if id, ok := value.(string); ok && id != "" {
			return id
		}
	}
	id := newRequestID()
	ctx.Set(requestIDContextKey, id)
	ctx.Writer.Header().Set(RequestIDHeader, id)
	return id
}

func newRequestID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err == nil {
		id[6] = (id[6] & 0x0f) | 0x40
		id[8] = (id[8] & 0x3f) | 0x80
		return hex.EncodeToString(id[0:4]) + "-" + hex.EncodeToString(id[4:6]) + "-" + hex.EncodeToString(id[6:8]) + "-" + hex.EncodeToString(id[8:10]) + "-" + hex.EncodeToString(id[10:16])
	}
	return "req-" + time.Now().UTC().Format("20060102150405.000000000") + "-" + strconv.FormatUint(fallbackIDCounter.Add(1), 10)
}

func logInternalFailure(ctx *gin.Context, status int, code, category string) {
	if ctx == nil {
		return
	}
	requestID := ensureRequestID(ctx)
	method := "unknown"
	if ctx.Request != nil && ctx.Request.Method != "" {
		method = ctx.Request.Method
	}
	operation := ctx.FullPath()
	if operation == "" {
		operation = "unmatched"
	}
	slog.Default().Error("HTTP request failure",
		"request_id", requestID,
		"method", method,
		"route", operation,
		"status_code", status,
		"public_error_code", code,
		"internal_error_category", category,
		"service_operation", operation,
	)
}
