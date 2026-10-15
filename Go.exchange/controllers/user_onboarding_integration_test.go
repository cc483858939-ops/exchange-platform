package controllers

import (
	"Go.exchange/global"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestUserOnboardingLifecycleIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserOnboarding{}, &models.UserOnboardingInterest{}); err != nil {
		t.Fatal(err)
	}
	previousDB := global.APIDb
	global.APIDb = db
	t.Cleanup(func() { global.APIDb = previousDB })

	var userIDs []uint
	t.Cleanup(func() {
		if len(userIDs) == 0 {
			return
		}
		if err := db.Where("user_id IN ?", userIDs).Delete(&models.UserOnboardingInterest{}).Error; err != nil {
			t.Errorf("clean onboarding interests: %v", err)
		}
		if err := db.Where("user_id IN ?", userIDs).Delete(&models.UserOnboarding{}).Error; err != nil {
			t.Errorf("clean onboarding states: %v", err)
		}
		if err := db.Unscoped().Delete(&models.User{}, "id IN ?", userIDs).Error; err != nil {
			t.Errorf("clean onboarding users: %v", err)
		}
	})

	users := []models.User{
		{Username: "onboarding-a-" + uuid.NewString(), Password: "unused"},
		{Username: "onboarding-b-" + uuid.NewString(), Password: "unused"},
		{Username: "onboarding-missing-" + uuid.NewString(), Password: "unused"},
	}
	for index := range users {
		if err := db.Create(&users[index]).Error; err != nil {
			t.Fatal(err)
		}
		userIDs = append(userIDs, users[index].ID)
		if index < 2 {
			if err := db.Create(&models.UserOnboarding{UserID: users[index].ID, CurrentStep: models.UserOnboardingStepAvatar}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}

	gin.SetMode(gin.TestMode)
	response := callUserOnboardingHandler(t, users[0].ID, http.MethodGet, "/api/me/onboarding", "", GetMyOnboarding)
	initial := decodeUserOnboardingStatus(t, response)
	if response.Code != http.StatusOK || initial.Completed || initial.CurrentStep != models.UserOnboardingStepAvatar || initial.CompletedAt != nil || len(initial.InterestSlugs) != 0 {
		t.Fatalf("unexpected initial status=%d response=%+v body=%s", response.Code, initial, response.Body.String())
	}

	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/progress", `{"current_step":"interests"}`, UpdateMyOnboardingProgress)
	progress := decodeUserOnboardingStatus(t, response)
	if response.Code != http.StatusOK || progress.CurrentStep != models.UserOnboardingStepInterests || progress.Completed {
		t.Fatalf("progress update status=%d response=%+v", response.Code, progress)
	}
	response = callUserOnboardingHandler(t, users[0].ID, http.MethodGet, "/api/me/onboarding", "", GetMyOnboarding)
	if restored := decodeUserOnboardingStatus(t, response); response.Code != http.StatusOK || restored.CurrentStep != models.UserOnboardingStepInterests {
		t.Fatalf("progress was not restored: status=%d response=%+v", response.Code, restored)
	}
	response = callUserOnboardingHandler(t, users[1].ID, http.MethodGet, "/api/me/onboarding", "", GetMyOnboarding)
	if other := decodeUserOnboardingStatus(t, response); response.Code != http.StatusOK || other.CurrentStep != models.UserOnboardingStepAvatar || other.Completed {
		t.Fatalf("onboarding state leaked across users: status=%d response=%+v", response.Code, other)
	}
	response = callUserOnboardingHandler(t, users[2].ID, http.MethodGet, "/api/me/onboarding", "", GetMyOnboarding)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("missing onboarding state was fabricated: status=%d body=%s", response.Code, response.Body.String())
	}
	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/progress", `{"current_step":"not-a-step"}`, UpdateMyOnboardingProgress)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid step status=%d body=%s", response.Code, response.Body.String())
	}
	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/progress", `{"current_step":"avatar","user_id":999999}`, UpdateMyOnboardingProgress)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("request target override status=%d body=%s", response.Code, response.Body.String())
	}

	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/interests", `{"topic_slugs":["space","ai"]}`, ReplaceMyOnboardingInterests)
	interestStatus := decodeUserOnboardingStatus(t, response)
	if response.Code != http.StatusOK || !reflect.DeepEqual(interestStatus.InterestSlugs, []string{"ai", "space"}) {
		t.Fatalf("interest save status=%d response=%+v", response.Code, interestStatus)
	}
	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/interests", `{"topic_slugs":["business"]}`, ReplaceMyOnboardingInterests)
	replaced := decodeUserOnboardingStatus(t, response)
	if response.Code != http.StatusOK || !reflect.DeepEqual(replaced.InterestSlugs, []string{"business"}) {
		t.Fatalf("interest replacement status=%d response=%+v", response.Code, replaced)
	}
	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/interests", `{"topic_slugs":["business"]}`, ReplaceMyOnboardingInterests)
	if repeated := decodeUserOnboardingStatus(t, response); response.Code != http.StatusOK || !reflect.DeepEqual(repeated.InterestSlugs, []string{"business"}) {
		t.Fatalf("repeated interest replacement status=%d response=%+v", response.Code, repeated)
	}
	for _, body := range []string{
		`{"topic_slugs":["ai","ai"]}`,
		`{"topic_slugs":["unknown-topic"]}`,
		`{"topic_slugs":[""]}`,
		`{}`,
	} {
		response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/interests", body, ReplaceMyOnboardingInterests)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid interests %s status=%d body=%s", body, response.Code, response.Body.String())
		}
	}

	failureCallback := "f15:onboarding-interest-create-failure-" + uuid.NewString()
	if err := db.Callback().Create().Before("gorm:create").Register(failureCallback, func(tx *gorm.DB) {
		if tx.Statement.Table == "user_onboarding_interests" {
			tx.AddError(errors.New("injected onboarding interest write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			db.Callback().Create().Remove(failureCallback)
		}
	})
	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/interests", `{"topic_slugs":["ai"]}`, ReplaceMyOnboardingInterests)
	db.Callback().Create().Remove(failureCallback)
	callbackRegistered = false
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("injected interest failure status=%d body=%s", response.Code, response.Body.String())
	}
	response = callUserOnboardingHandler(t, users[0].ID, http.MethodGet, "/api/me/onboarding", "", GetMyOnboarding)
	if retained := decodeUserOnboardingStatus(t, response); response.Code != http.StatusOK || !reflect.DeepEqual(retained.InterestSlugs, []string{"business"}) {
		t.Fatalf("failed replacement did not roll back: status=%d response=%+v", response.Code, retained)
	}

	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/interests", `{"topic_slugs":[]}`, ReplaceMyOnboardingInterests)
	if cleared := decodeUserOnboardingStatus(t, response); response.Code != http.StatusOK || cleared.InterestSlugs == nil || len(cleared.InterestSlugs) != 0 {
		t.Fatalf("empty interests did not clear set: status=%d response=%+v", response.Code, cleared)
	}
	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/interests", `{"topic_slugs":["ai","business"]}`, ReplaceMyOnboardingInterests)
	if response.Code != http.StatusOK {
		t.Fatalf("prepare completion interests status=%d body=%s", response.Code, response.Body.String())
	}
	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/complete", "", CompleteMyOnboarding)
	completed := decodeUserOnboardingStatus(t, response)
	if response.Code != http.StatusOK || !completed.Completed || completed.CompletedAt == nil || completed.CurrentStep != models.UserOnboardingStepInterests || !reflect.DeepEqual(completed.InterestSlugs, []string{"ai", "business"}) {
		t.Fatalf("completion status=%d response=%+v", response.Code, completed)
	}
	firstCompletionAt := *completed.CompletedAt
	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/complete", "", CompleteMyOnboarding)
	repeatedCompletion := decodeUserOnboardingStatus(t, response)
	if response.Code != http.StatusOK || repeatedCompletion.CompletedAt == nil || !repeatedCompletion.CompletedAt.Equal(firstCompletionAt) {
		t.Fatalf("repeat completion changed completed_at: status=%d response=%+v", response.Code, repeatedCompletion)
	}
	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/progress", `{"current_step":"avatar"}`, UpdateMyOnboardingProgress)
	terminal := decodeUserOnboardingStatus(t, response)
	if response.Code != http.StatusOK || !terminal.Completed || terminal.CurrentStep != models.UserOnboardingStepInterests || terminal.CompletedAt == nil || !terminal.CompletedAt.Equal(firstCompletionAt) {
		t.Fatalf("completed onboarding reopened: status=%d response=%+v", response.Code, terminal)
	}
	response = callUserOnboardingHandler(t, users[0].ID, http.MethodPut, "/api/me/onboarding/interests", `{"topic_slugs":["space"]}`, ReplaceMyOnboardingInterests)
	updatedAfterComplete := decodeUserOnboardingStatus(t, response)
	if response.Code != http.StatusOK || !updatedAfterComplete.Completed || !reflect.DeepEqual(updatedAfterComplete.InterestSlugs, []string{"space"}) || updatedAfterComplete.CompletedAt == nil || !updatedAfterComplete.CompletedAt.Equal(firstCompletionAt) {
		t.Fatalf("completed user's interests could not be updated: status=%d response=%+v", response.Code, updatedAfterComplete)
	}
	response = callUserOnboardingHandler(t, users[1].ID, http.MethodGet, "/api/me/onboarding", "", GetMyOnboarding)
	if other := decodeUserOnboardingStatus(t, response); response.Code != http.StatusOK || other.Completed || other.CurrentStep != models.UserOnboardingStepAvatar || len(other.InterestSlugs) != 0 {
		t.Fatalf("second user's state changed: status=%d response=%+v", response.Code, other)
	}
}

func callUserOnboardingHandler(t *testing.T, userID uint, method, path, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if body != "" {
		ctx.Request.Header.Set("Content-Type", "application/json")
	}
	ctx.Set("user_id", userID)
	handler(ctx)
	return recorder
}

func decodeUserOnboardingStatus(t *testing.T, recorder *httptest.ResponseRecorder) userOnboardingStatusResponse {
	t.Helper()
	var response userOnboardingStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode onboarding response: %v; body=%s", err, recorder.Body.String())
	}
	return response
}
