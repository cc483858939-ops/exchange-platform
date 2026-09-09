package translation

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"Go.exchange/metrics"

	"golang.org/x/sync/singleflight"
)

const (
	DefaultBaseTTL        = 7 * 24 * time.Hour
	DefaultCacheJitter    = 24 * time.Hour
	DefaultMaxSourceRunes = 2000
)

type ServiceConfig struct {
	Enabled        bool
	BaseURL        string
	Model          string
	PromptVersion  string
	BaseTTL        time.Duration
	CacheJitter    time.Duration
	MaxSourceRunes int
	Jitter         JitterFunc
}

type TranslationService struct {
	provider Provider
	cache    Cache
	config   ServiceConfig
	// group is process-local; Redis is only the durable derived-data cache in v1.
	group singleflight.Group
}

type cachedValue struct {
	Translation    string `json:"translation"`
	SourceLanguage string `json:"source_language"`
	TargetLanguage string `json:"target_language"`
}

func NewService(provider Provider, cache Cache, config ServiceConfig) *TranslationService {
	if strings.TrimSpace(config.PromptVersion) == "" {
		config.PromptVersion = DefaultPromptVersion
	}
	if config.BaseTTL <= 0 {
		config.BaseTTL = DefaultBaseTTL
	}
	if config.MaxSourceRunes <= 0 {
		config.MaxSourceRunes = DefaultMaxSourceRunes
	}
	if config.Jitter == nil {
		config.Jitter = cryptoJitter
	}
	return &TranslationService{provider: provider, cache: cache, config: config}
}

func (s *TranslationService) Translate(ctx context.Context, postID uint, content, sourceLanguage, targetLanguage string) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now()
	source := NormalizeSourceLanguage(sourceLanguage)
	target, validTarget := NormalizeTargetLanguage(targetLanguage)
	outcome := "error"
	defer func() {
		metrics.ObserveTranslationRequestDuration(time.Since(started))
		metrics.RecordTranslationRequest(outcome, source, target)
	}()

	if !validTarget {
		return Result{}, ErrInvalidTargetLanguage
	}
	if source == target {
		outcome = "same_language"
		return Result{
			Translation: content, SourceLanguage: source, TargetLanguage: target, Translated: false,
		}, nil
	}
	if !s.config.Enabled {
		outcome = "disabled"
		return Result{}, ErrFeatureDisabled
	}
	if utf8.RuneCountInString(content) > s.config.MaxSourceRunes {
		outcome = "source_too_long"
		return Result{}, ErrSourceTooLong
	}

	key := CacheKey(postID, content, source, target, BackendIdentity(s.config.BaseURL, s.config.Model), s.config.PromptVersion)
	if cached, found, _ := s.readCache(key, source, target); found {
		outcome = "cache_hit"
		return cached, nil
	}

	resultChannel := s.group.DoChan(key, func() (interface{}, error) {
		// The second lookup is mandatory: another request can have populated
		// Redis between the first read and this process-local flight.
		if cached, found, _ := s.readCache(key, source, target); found {
			return cached, nil
		}
		if s.provider == nil {
			return nil, newProviderError(ProviderErrorMisconfigured, 0, ErrProviderMisconfigured)
		}

		providerContext := context.WithoutCancel(ctx)
		providerStarted := time.Now()
		providerResult, err := s.provider.Translate(providerContext, Request{
			Content: content, SourceLanguage: source, TargetLanguage: target,
		})
		metrics.ObserveTranslationProviderDuration(time.Since(providerStarted))
		if err != nil {
			errorClass := providerOutcome(err)
			metrics.RecordTranslationProviderRequest(errorClass)
			statusCode := 0
			var providerError *ProviderError
			if errors.As(err, &providerError) {
				statusCode = providerError.StatusCode
			}
			log.Printf("[Translation] client=openai_compatible model=%s target_language=%s error_class=%s upstream_status=%d", s.config.Model, target, errorClass, statusCode)
			if !isKnownProviderError(err) {
				return nil, newProviderError(ProviderErrorUnavailable, 0, err)
			}
			return nil, err
		}
		metrics.RecordTranslationProviderRequest("success")

		translated := strings.TrimSpace(providerResult.Translation)
		if translated == "" {
			return nil, newProviderError(ProviderErrorInvalidResponse, 0, nil)
		}
		result := Result{
			Translation: translated, SourceLanguage: source, TargetLanguage: target, Translated: true,
		}

		if s.cache != nil {
			payload, marshalErr := json.Marshal(cachedValue{
				Translation: translated, SourceLanguage: source, TargetLanguage: target,
			})
			if marshalErr != nil {
				metrics.RecordTranslationCacheOperation("set", "error")
			} else {
				expiration := s.config.BaseTTL
				if s.config.CacheJitter > 0 {
					jitter := s.config.Jitter(s.config.CacheJitter)
					if jitter > 0 && jitter < s.config.CacheJitter {
						expiration += jitter
					} else if jitter >= s.config.CacheJitter {
						expiration += s.config.CacheJitter - time.Nanosecond
					}
				}
				if setErr := s.cache.Set(key, string(payload), expiration); setErr != nil {
					metrics.RecordTranslationCacheOperation("set", "error")
					log.Printf("[Translation] cache set failed for post %d: %v", postID, setErr)
				} else {
					metrics.RecordTranslationCacheOperation("set", "success")
				}
			}
		}
		return result, nil
	})

	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case flightResult := <-resultChannel:
		if flightResult.Err != nil {
			outcome = "provider_error"
			return Result{}, flightResult.Err
		}
		result, ok := flightResult.Val.(Result)
		if !ok {
			outcome = "provider_error"
			return Result{}, newProviderError(ProviderErrorInvalidResponse, 0, errors.New("translation result type mismatch"))
		}
		if result.Translated {
			outcome = "generated"
		} else {
			outcome = "same_language"
		}
		return result, nil
	}
}

func (s *TranslationService) readCache(key, source, target string) (Result, bool, error) {
	if s.cache == nil {
		return Result{}, false, nil
	}
	raw, err := s.cache.Get(key)
	if err != nil {
		if isRedisCacheMiss(err) {
			metrics.RecordTranslationCacheOperation("get", "miss")
			return Result{}, false, nil
		}
		metrics.RecordTranslationCacheOperation("get", "error")
		log.Printf("[Translation] cache get failed: %v", err)
		return Result{}, false, err
	}
	var value cachedValue
	if err := json.Unmarshal([]byte(raw), &value); err != nil ||
		(value.SourceLanguage != LanguageChinese &&
			value.SourceLanguage != LanguageJapanese &&
			value.SourceLanguage != LanguageEnglish &&
			value.SourceLanguage != LanguageUndetermined) ||
		strings.TrimSpace(value.Translation) == "" ||
		value.SourceLanguage != source ||
		value.TargetLanguage != target {
		metrics.RecordTranslationCacheOperation("get", "incompatible")
		return Result{}, false, nil
	}
	metrics.RecordTranslationCacheOperation("get", "hit")
	return Result{
		Translation: strings.TrimSpace(value.Translation), SourceLanguage: source,
		TargetLanguage: target, Translated: true,
	}, true, nil
}

func isKnownProviderError(err error) bool {
	return errors.Is(err, ErrProviderRateLimited) ||
		errors.Is(err, ErrProviderTimeout) ||
		errors.Is(err, ErrProviderUnavailable) ||
		errors.Is(err, ErrProviderInvalidResponse) ||
		errors.Is(err, ErrProviderMisconfigured)
}

func providerOutcome(err error) string {
	switch {
	case errors.Is(err, ErrProviderRateLimited):
		return "rate_limited"
	case errors.Is(err, ErrProviderTimeout):
		return "timeout"
	case errors.Is(err, ErrProviderInvalidResponse):
		return "invalid_response"
	case errors.Is(err, ErrProviderMisconfigured):
		return "misconfigured"
	default:
		return "upstream_error"
	}
}

func cryptoJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	upperBound := big.NewInt(int64(max))
	value, err := rand.Int(rand.Reader, upperBound)
	if err != nil {
		return 0
	}
	return time.Duration(value.Int64())
}
