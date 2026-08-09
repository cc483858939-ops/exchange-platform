package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newUserControllerContext(path, id string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	ctx.Params = gin.Params{{Key: "id", Value: id}}
	return ctx, recorder
}

func TestUserPublicEndpointsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}); err != nil {
		t.Fatal(err)
	}

	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	target := models.User{Username: "profile-target-" + uuid.NewString(), Password: "secret"}
	other := models.User{Username: "profile-other-" + uuid.NewString(), Password: "secret"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	expiredAt := now.Add(-time.Hour)
	articles := []models.Article{
		{AuthorID: target.ID, Title: "older", Preview: "p", LikeCount: 9, CommentCount: 4, Model: gorm.Model{CreatedAt: now.Add(-time.Hour)}},
		{AuthorID: target.ID, Title: "newer", Preview: "p", LikeCount: 17, CommentCount: 8, Model: gorm.Model{CreatedAt: now}},
		{AuthorID: target.ID, Title: "expired", Preview: "p", ExpiredAt: &expiredAt, Model: gorm.Model{CreatedAt: now.Add(time.Hour)}},
		{AuthorID: other.ID, Title: "other", Preview: "p", Model: gorm.Model{CreatedAt: now.Add(2 * time.Hour)}},
	}
	if err := db.Create(&articles).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := []uint{articles[0].ID, articles[1].ID, articles[2].ID, articles[3].ID}
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Article{})
		db.Unscoped().Where("id IN ?", []uint{target.ID, other.ID}).Delete(&models.User{})
	})

	ctx, recorder := newUserControllerContext("/api/users/"+strconvUint(target.ID), strconvUint(target.ID))
	GetUserByID(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("profile status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var profile map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile["username"] != target.Username || profile["id"] == nil {
		t.Fatalf("profile=%v", profile)
	}
	for _, forbidden := range []string{"password", "Password", "DeletedAt", "refresh_token", "AuthorID"} {
		if _, exists := profile[forbidden]; exists {
			t.Fatalf("profile leaked %s: %v", forbidden, profile)
		}
	}

	ctx, recorder = newUserControllerContext("/api/users/"+strconvUint(target.ID)+"/articles?limit=20&offset=0", strconvUint(target.ID))
	GetUserArticles(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("articles status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response []articleResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response) != 2 || response[0].Title != "newer" || response[1].Title != "older" || response[0].LikeCount != 17 || response[0].CommentCount != 8 {
		t.Fatalf("unexpected author articles: %#v", response)
	}
	for _, article := range response {
		if article.Author.ID != target.ID {
			t.Fatalf("foreign author in profile response: %#v", article.Author)
		}
	}

	for _, invalid := range []string{"0", "-1", "not-a-number"} {
		ctx, recorder = newUserControllerContext("/api/users/"+invalid, invalid)
		GetUserByID(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid id %q status=%d", invalid, recorder.Code)
		}
	}
	ctx, recorder = newUserControllerContext("/api/users/"+strconvUint(target.ID)+"/articles?offset=-1", strconvUint(target.ID))
	GetUserArticles(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid offset status=%d", recorder.Code)
	}
	ctx, recorder = newUserControllerContext("/api/users/999999999", "999999999")
	GetUserByID(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing user status=%d", recorder.Code)
	}
}
