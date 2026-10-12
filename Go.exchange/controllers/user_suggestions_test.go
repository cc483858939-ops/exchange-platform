package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestGetSuggestedUsersContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalActive := loadActiveProfileViewer
	originalLoader := loadSuggestedUsers
	t.Cleanup(func() {
		loadActiveProfileViewer = originalActive
		loadSuggestedUsers = originalLoader
	})
	loadActiveProfileViewer = func(_ context.Context, id uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: id}}, nil
	}

	var gotViewer uint
	var gotLimit int
	loadSuggestedUsers = func(_ context.Context, viewerID uint, limit int) (userConnectionPageResponse, error) {
		gotViewer, gotLimit = viewerID, limit
		return userConnectionPageResponse{Items: []userConnectionResponse{{
			User: publicUserSummaryResponse{ID: 19, Username: "alice", DisplayName: "Alice"},
		}}, HasMore: false}, nil
	}

	ctx, recorder := newSuggestedUsersUnitContext("", uint(42))
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusOK || gotViewer != 42 || gotLimit != defaultSuggestedUserLimit {
		t.Fatalf("default status=%d viewer=%d limit=%d body=%s", recorder.Code, gotViewer, gotLimit, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["has_more"] != false {
		t.Fatalf("response=%v", payload)
	}
	items, ok := payload["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items=%v", payload["items"])
	}
	user, ok := items[0].(map[string]any)["user"].(map[string]any)
	if !ok || user["username"] != "alice" {
		t.Fatalf("user response=%v", items[0])
	}
	for _, privateField := range []string{"password", "cover_image_url", "follower_count"} {
		if _, exists := user[privateField]; exists {
			t.Fatalf("response exposed %q: %v", privateField, user)
		}
	}

	ctx, recorder = newSuggestedUsersUnitContext("?limit=5", uint(42))
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusOK || gotLimit != maxSuggestedUserLimit {
		t.Fatalf("max limit status=%d got=%d body=%s", recorder.Code, gotLimit, recorder.Body.String())
	}

	for _, query := range []string{"?limit=", "?limit=0", "?limit=-1", "?limit=6", "?limit=nope", "?limit=3&limit=4", "?offset=0"} {
		ctx, recorder = newSuggestedUsersUnitContext(query, uint(42))
		GetSuggestedUsers(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("query %q status=%d body=%s, want 400", query, recorder.Code, recorder.Body.String())
		}
	}
}

func TestGetSuggestedUsersAuthenticationAndDatabaseErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalActive := loadActiveProfileViewer
	originalLoader := loadSuggestedUsers
	t.Cleanup(func() {
		loadActiveProfileViewer = originalActive
		loadSuggestedUsers = originalLoader
	})
	loadSuggestedUsers = func(context.Context, uint, int) (userConnectionPageResponse, error) {
		t.Fatal("suggestion query should not run")
		return userConnectionPageResponse{}, nil
	}

	loadActiveProfileViewer = func(context.Context, uint) (models.User, error) {
		return models.User{}, errors.New("inactive lookup unavailable")
	}
	ctx, recorder := newSuggestedUsersUnitContext("", nil)
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing viewer status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	ctx, recorder = newSuggestedUsersUnitContext("", true)
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid viewer identity status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	loadActiveProfileViewer = func(context.Context, uint) (models.User, error) {
		return models.User{}, gorm.ErrRecordNotFound
	}
	ctx, recorder = newSuggestedUsersUnitContext("", uint(42))
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("inactive viewer status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	loadActiveProfileViewer = func(_ context.Context, id uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: id}}, nil
	}
	loadSuggestedUsers = func(context.Context, uint, int) (userConnectionPageResponse, error) {
		return userConnectionPageResponse{}, errors.New("database exploded")
	}
	ctx, recorder = newSuggestedUsersUnitContext("", uint(42))
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "exploded") {
		t.Fatalf("query error status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	loadSuggestedUsers = func(context.Context, uint, int) (userConnectionPageResponse, error) {
		return userConnectionPageResponse{}, nil
	}
	ctx, recorder = newSuggestedUsersUnitContext("", uint(42))
	GetSuggestedUsers(ctx)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"items":[]`) || !strings.Contains(recorder.Body.String(), `"has_more":false`) {
		t.Fatalf("empty response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func newSuggestedUsersUnitContext(query string, viewerID any) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/users/suggestions"+query, nil)
	if viewerID != nil {
		ctx.Set("user_id", viewerID)
	}
	return ctx, recorder
}

func TestSuggestedUserRankingPreservesColdStartOrder(t *testing.T) {
	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	rows := []suggestedUserQueryRow{
		{UserID: 3, RecentPostCount: 2, LatestPostAt: now.Add(-time.Minute)},
		{UserID: 2, RecentPostCount: 5, LatestPostAt: now.Add(-2 * time.Minute)},
		{UserID: 4, RecentPostCount: 5, LatestPostAt: now.Add(-time.Minute)},
		{UserID: 1, RecentPostCount: 5, LatestPostAt: now.Add(-time.Minute)},
	}

	ranked := rankSuggestedUserRows(rows, now, 6, 5)
	if got, want := suggestedUserIDs(ranked), []uint{1, 4, 2, 3}; !equalSuggestedUserIDs(got, want) {
		t.Fatalf("cold-start order=%v want=%v", got, want)
	}
}

func TestSuggestedUserAffinityScaleUsesRuntimeConfigAndDefault(t *testing.T) {
	originalConfig := config.AppConfig
	t.Cleanup(func() { config.AppConfig = originalConfig })

	config.AppConfig = nil
	if got, want := suggestedUserAffinitySaturationScale(), 6.0; got != want {
		t.Fatalf("default affinity scale=%g want=%g", got, want)
	}
	config.AppConfig = &config.Config{
		Recommendation: config.RecommendationConfig{AuthorAffinitySaturationScale: 2.5},
	}
	if got, want := suggestedUserAffinitySaturationScale(), 2.5; got != want {
		t.Fatalf("runtime affinity scale=%g want=%g", got, want)
	}
}

func TestSuggestedUserRankingUsesAffinityBeforeApplyingLimit(t *testing.T) {
	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	rows := []suggestedUserQueryRow{
		{UserID: 1, RecentPostCount: 5, LatestPostAt: now.Add(-time.Minute)},
		{UserID: 2, RecentPostCount: 5, LatestPostAt: now.Add(-time.Minute)},
		{UserID: 3, RecentPostCount: 5, LatestPostAt: now.Add(-time.Minute)},
		{UserID: 4, RecentPostCount: 5, LatestPostAt: now.Add(-time.Minute)},
		{UserID: 5, RecentPostCount: 5, LatestPostAt: now.Add(-time.Minute)},
		{UserID: 6, RecentPostCount: 1, LatestPostAt: now, RawAffinity: 1000},
	}

	ranked := rankSuggestedUserRows(rows, now, 6, 5)
	if len(ranked) != 5 {
		t.Fatalf("ranked count=%d want=5", len(ranked))
	}
	if ranked[0].UserID != 6 {
		t.Fatalf("personalized first user=%d want high-affinity author 6", ranked[0].UserID)
	}
}

func TestSuggestedUserAffinityScoreSaturatesAndUsesConfiguredScale(t *testing.T) {
	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	row := suggestedUserQueryRow{RecentPostCount: 5, LatestPostAt: now, RawAffinity: 6}
	if got, want := suggestedUserFinalScore(row, now, 6), 1+suggestedUserAffinityWeight*math.Tanh(1); math.Abs(got-want) > 1e-12 {
		t.Fatalf("score=%g want=%g", got, want)
	}
	row.RawAffinity = 1e12
	if got, want := suggestedUserFinalScore(row, now, 6), 1+suggestedUserAffinityWeight; math.Abs(got-want) > 1e-12 {
		t.Fatalf("saturated score=%g want=%g", got, want)
	}
}

func TestSuggestedUserRankingIgnoresNonPositiveOrInvalidAffinity(t *testing.T) {
	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	rows := []suggestedUserQueryRow{
		{UserID: 4, RecentPostCount: 1, LatestPostAt: now, RawAffinity: math.Inf(1)},
		{UserID: 3, RecentPostCount: 2, LatestPostAt: now.Add(-time.Minute), RawAffinity: math.NaN()},
		{UserID: 2, RecentPostCount: 3, LatestPostAt: now.Add(-2 * time.Minute), RawAffinity: math.Inf(-1)},
		{UserID: 1, RecentPostCount: 4, LatestPostAt: now.Add(-3 * time.Minute), RawAffinity: -2},
		{UserID: 5, RecentPostCount: 0, LatestPostAt: now},
	}

	ranked := rankSuggestedUserRows(rows, now, 6, 5)
	if got, want := suggestedUserIDs(ranked), []uint{1, 2, 3, 4, 5}; !equalSuggestedUserIDs(got, want) {
		t.Fatalf("invalid-affinity order=%v want=%v", got, want)
	}
	for _, rawAffinity := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1), 0} {
		if score := suggestedUserFinalScore(suggestedUserQueryRow{RawAffinity: rawAffinity}, now, 6); score != 0 {
			t.Errorf("affinity %v added score %v", rawAffinity, score)
		}
	}
}

func TestSuggestedUserRankingBreaksPersonalizedTiesByActivityAndUserID(t *testing.T) {
	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	rows := []suggestedUserQueryRow{
		{UserID: 3, RecentPostCount: 2, LatestPostAt: now, RawAffinity: 6},
		{UserID: 1, RecentPostCount: 2, LatestPostAt: now, RawAffinity: 6},
		{UserID: 2, RecentPostCount: 2, LatestPostAt: now, RawAffinity: 6},
		{UserID: 4, RecentPostCount: 1, LatestPostAt: now, RawAffinity: 6},
	}

	ranked := rankSuggestedUserRows(rows, now, 6, 5)
	if got, want := suggestedUserIDs(ranked), []uint{1, 2, 3, 4}; !equalSuggestedUserIDs(got, want) {
		t.Fatalf("personalized tie order=%v want=%v", got, want)
	}
}

func suggestedUserIDs(rows []suggestedUserQueryRow) []uint {
	ids := make([]uint, len(rows))
	for index, row := range rows {
		ids[index] = row.UserID
	}
	return ids
}

func equalSuggestedUserIDs(left, right []uint) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
