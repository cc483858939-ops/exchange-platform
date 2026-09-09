package translation

import (
	"context"
	"errors"
	"time"
)

var (
	ErrFeatureDisabled         = errors.New("translation feature is disabled")
	ErrInvalidTargetLanguage   = errors.New("invalid translation target language")
	ErrSourceTooLong           = errors.New("translation source content is too long")
	ErrCacheUnavailable        = errors.New("translation cache is unavailable")
	ErrProviderRateLimited     = errors.New("translation provider rate limited")
	ErrProviderTimeout         = errors.New("translation provider timed out")
	ErrProviderUnavailable     = errors.New("translation provider unavailable")
	ErrProviderInvalidResponse = errors.New("translation provider returned an invalid response")
	ErrProviderMisconfigured   = errors.New("translation provider is misconfigured")
)

// Provider translates one post. Provider implementations must not accept
// caller-supplied prompts, models, or provider credentials.
type Provider interface {
	Translate(ctx context.Context, req Request) (ProviderResult, error)
}

type Request struct {
	Content        string
	SourceLanguage string
	TargetLanguage string
}

type ProviderResult struct {
	Translation      string
	PromptTokens     int
	CompletionTokens int
}

type Result struct {
	Translation    string
	SourceLanguage string
	TargetLanguage string
	Translated     bool
}

type Service interface {
	Translate(ctx context.Context, postID uint, content, sourceLanguage, targetLanguage string) (Result, error)
}

// Cache is deliberately small so the translation service can be tested
// without a Redis server. RedisCache is the production implementation.
type Cache interface {
	Get(key string) (string, error)
	Set(key, value string, expiration time.Duration) error
}

type JitterFunc func(max time.Duration) time.Duration

type ProviderErrorKind string

const (
	ProviderErrorRateLimited     ProviderErrorKind = "rate_limited"
	ProviderErrorTimeout         ProviderErrorKind = "timeout"
	ProviderErrorUnavailable     ProviderErrorKind = "unavailable"
	ProviderErrorInvalidResponse ProviderErrorKind = "invalid_response"
	ProviderErrorMisconfigured   ProviderErrorKind = "misconfigured"
)

// ProviderError preserves a small, typed taxonomy for controller mapping
// while keeping upstream response bodies and credentials out of public errors.
type ProviderError struct {
	Kind             ProviderErrorKind
	StatusCode       int
	RetryAfter       time.Duration
	RetryAfterHeader string
	cause            error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "translation provider error"
	}
	switch e.Kind {
	case ProviderErrorRateLimited:
		return ErrProviderRateLimited.Error()
	case ProviderErrorTimeout:
		return ErrProviderTimeout.Error()
	case ProviderErrorUnavailable:
		return ErrProviderUnavailable.Error()
	case ProviderErrorInvalidResponse:
		return ErrProviderInvalidResponse.Error()
	case ProviderErrorMisconfigured:
		return ErrProviderMisconfigured.Error()
	default:
		return "translation provider error"
	}
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *ProviderError) Is(target error) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ProviderErrorRateLimited:
		return target == ErrProviderRateLimited
	case ProviderErrorTimeout:
		return target == ErrProviderTimeout
	case ProviderErrorUnavailable:
		return target == ErrProviderUnavailable
	case ProviderErrorInvalidResponse:
		return target == ErrProviderInvalidResponse
	case ProviderErrorMisconfigured:
		return target == ErrProviderMisconfigured
	default:
		return false
	}
}

func newProviderError(kind ProviderErrorKind, statusCode int, cause error) *ProviderError {
	return &ProviderError{Kind: kind, StatusCode: statusCode, cause: cause}
}
