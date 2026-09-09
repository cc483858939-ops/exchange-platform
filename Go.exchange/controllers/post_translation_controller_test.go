package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"Go.exchange/translation"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type fakePostTranslationService struct {
	result translation.Result
	err    error
	calls  int
}

func (s *fakePostTranslationService) Translate(_ context.Context, _ uint, _, _, _ string) (translation.Result, error) {
	s.calls++
	return s.result, s.err
}

func newPostTranslationContext(id, body string, viewerID *uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts/"+id+"/translation", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: id}}
	if viewerID != nil {
		ctx.Set("user_id", *viewerID)
	}
	return ctx, recorder
}

func stubPostTranslationLoader(t *testing.T, loader func(*gorm.DB, uint) (postTranslationFields, error)) {
	t.Helper()
	original := loadPostTranslationFields
	t.Cleanup(func() { loadPostTranslationFields = original })
	loadPostTranslationFields = loader
}

func TestTranslatePostRequiresAuthenticationAndStrictInput(t *testing.T) {
	viewerID := uint(7)
	service := &fakePostTranslationService{}

	unauthenticated, recorder := newPostTranslationContext("42", `{"target_language":"en"}`, nil)
	translatePost(unauthenticated, service)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", recorder.Code)
	}

	for _, testCase := range []struct {
		name string
		id   string
		body string
	}{
		{name: "invalid id", id: "abc", body: `{"target_language":"en"}`},
		{name: "invalid target", id: "42", body: `{"target_language":"fr"}`},
		{name: "unknown field", id: "42", body: `{"target_language":"en","extra":true}`},
		{name: "multiple values", id: "42", body: `{"target_language":"en"}{}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, recorder := newPostTranslationContext(testCase.id, testCase.body, &viewerID)
			translatePost(ctx, service)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestTranslatePostReturnsSameLanguageLocally(t *testing.T) {
	viewerID := uint(7)
	service := &fakePostTranslationService{}
	stubPostTranslationLoader(t, func(_ *gorm.DB, postID uint) (postTranslationFields, error) {
		if postID != 42 {
			t.Fatalf("loader post id = %d", postID)
		}
		return postTranslationFields{ID: 42, Content: "Already English", Language: "en"}, nil
	})

	ctx, recorder := newPostTranslationContext("42", `{"target_language":"en"}`, &viewerID)
	translatePost(ctx, service)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var response postTranslationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response != (postTranslationResponse{
		PostID: 42, SourceLanguage: "en", TargetLanguage: "en", Translated: false, Translation: "Already English",
	}) {
		t.Fatalf("response = %+v", response)
	}
	if service.calls != 0 {
		t.Fatalf("service calls = %d, want 0", service.calls)
	}
}

func TestTranslatePostLoadsCanonicalFieldsAndReturnsServiceResult(t *testing.T) {
	viewerID := uint(7)
	service := &fakePostTranslationService{result: translation.Result{
		Translation:    "你好",
		SourceLanguage: "ja",
		TargetLanguage: "zh",
		Translated:     true,
	}}
	var loadedID uint
	stubPostTranslationLoader(t, func(_ *gorm.DB, postID uint) (postTranslationFields, error) {
		loadedID = postID
		return postTranslationFields{ID: postID, Content: "こんにちは", Language: "ja"}, nil
	})

	ctx, recorder := newPostTranslationContext("42", `{"target_language":"zh"}`, &viewerID)
	translatePost(ctx, service)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if loadedID != 42 || service.calls != 1 {
		t.Fatalf("loaded id = %d service calls = %d", loadedID, service.calls)
	}
	var response postTranslationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Translation != "你好" || !response.Translated || response.TargetLanguage != "zh" {
		t.Fatalf("response = %+v", response)
	}
}

func TestTranslatePostMapsVisibilityAndServiceErrors(t *testing.T) {
	viewerID := uint(7)
	stubPostTranslationLoader(t, func(_ *gorm.DB, _ uint) (postTranslationFields, error) {
		return postTranslationFields{}, gorm.ErrRecordNotFound
	})
	ctx, recorder := newPostTranslationContext("42", `{"target_language":"en"}`, &viewerID)
	translatePost(ctx, &fakePostTranslationService{})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("not found status = %d", recorder.Code)
	}

	stubPostTranslationLoader(t, func(_ *gorm.DB, _ uint) (postTranslationFields, error) {
		return postTranslationFields{ID: 42, Content: "content", Language: "zh"}, nil
	})
	for _, testCase := range []struct {
		name       string
		err        error
		statusCode int
		retryAfter string
	}{
		{name: "too long", err: translation.ErrSourceTooLong, statusCode: http.StatusUnprocessableEntity},
		{name: "provider timeout", err: &translation.ProviderError{Kind: translation.ProviderErrorTimeout}, statusCode: http.StatusServiceUnavailable},
		{name: "provider rate limit", err: &translation.ProviderError{Kind: translation.ProviderErrorRateLimited, RetryAfterHeader: "17"}, statusCode: http.StatusTooManyRequests, retryAfter: "17"},
		{name: "unexpected", err: errors.New("unexpected internal detail"), statusCode: http.StatusInternalServerError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := &fakePostTranslationService{err: testCase.err}
			ctx, recorder := newPostTranslationContext("42", `{"target_language":"en"}`, &viewerID)
			translatePost(ctx, service)
			if recorder.Code != testCase.statusCode {
				t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
			}
			if testCase.retryAfter != "" && recorder.Header().Get("Retry-After") != testCase.retryAfter {
				t.Fatalf("Retry-After = %q", recorder.Header().Get("Retry-After"))
			}
			if strings.Contains(recorder.Body.String(), "unexpected internal detail") {
				t.Fatalf("internal error leaked: %s", recorder.Body.String())
			}
		})
	}
}
