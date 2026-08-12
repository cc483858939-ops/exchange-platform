package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
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
		{AuthorID: target.ID, Title: "newer", Content: "Canonical profile body", Preview: "p", LikeCount: 17, CommentCount: 8, Model: gorm.Model{CreatedAt: now}},
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
	if len(response) != 2 || response[0].Title != "newer" || response[0].Content != "Canonical profile body" || response[1].Title != "older" || response[0].LikeCount != 17 || response[0].CommentCount != 8 {
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

func TestEditableUserProfileIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}

	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	target := models.User{
		Username:    "editable-target-" + uuid.NewString(),
		Password:    "secret",
		DisplayName: "Initial name",
		Bio:         "Initial bio",
		AvatarURL:   "",
	}
	other := models.User{Username: "editable-other-" + uuid.NewString(), Password: "secret"}
	deletedViewer := models.User{Username: "editable-deleted-" + uuid.NewString(), Password: "secret"}
	for _, user := range []*models.User{&target, &other, &deletedViewer} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		db.Unscoped().Where("id IN ?", []uint{target.ID, other.ID, deletedViewer.ID}).Delete(&models.User{})
	})

	getContext, getRecorder := newUserControllerContext("/api/users/"+strconvUint(target.ID), strconvUint(target.ID))
	GetUserByID(getContext)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("initial profile status=%d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	var initial publicUserResponse
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.ID != target.ID || initial.Username != target.Username || initial.DisplayName != target.DisplayName || initial.Bio != target.Bio || initial.AvatarURL != target.AvatarURL {
		t.Fatalf("initial public profile=%#v", initial)
	}

	avatarURL := "/api/files/profile-avatars/" + strconvUint(target.ID) + "/550e8400-e29b-41d4-a716-446655440000.webp"
	ctx, recorder := newUserProfilePatchIntegrationContext(strconvUint(target.ID), `{"display_name":"  Updated name  ","bio":"  Updated bio  ","avatar_url":"`+avatarURL+`"}`, target.ID)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("full patch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var updated publicUserResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Updated name" || updated.Bio != "Updated bio" || updated.AvatarURL != avatarURL {
		t.Fatalf("updated public profile=%#v", updated)
	}
	var persisted models.User
	if err := db.First(&persisted, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.DisplayName != "Updated name" || persisted.Bio != "Updated bio" || persisted.AvatarURL != avatarURL {
		t.Fatalf("persisted profile=%#v", persisted)
	}

	ctx, recorder = newUserProfilePatchIntegrationContext(strconvUint(target.ID), `{"display_name":"Name only"}`, target.ID)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("display-only patch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Name only" || updated.Bio != "Updated bio" || updated.AvatarURL != avatarURL {
		t.Fatalf("display-only patch lost fields: %#v", updated)
	}

	ctx, recorder = newUserProfilePatchIntegrationContext(strconvUint(target.ID), `{"bio":"Bio only"}`, target.ID)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("bio-only patch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Name only" || updated.Bio != "Bio only" || updated.AvatarURL != avatarURL {
		t.Fatalf("bio-only patch lost fields: %#v", updated)
	}

	ctx, recorder = newUserProfilePatchIntegrationContext(strconvUint(target.ID), `{"avatar_url":""}`, target.ID)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("avatar clear status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.AvatarURL != "" || updated.DisplayName != "Name only" || updated.Bio != "Bio only" {
		t.Fatalf("avatar clear changed unrelated fields: %#v", updated)
	}

	for _, body := range []string{
		`{"display_name":"" ,"bio":"" ,"avatar_url":""}`,
	} {
		ctx, recorder = newUserProfilePatchIntegrationContext(strconvUint(target.ID), body, target.ID)
		UpdateUserProfile(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("clear patch status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	if err := db.First(&persisted, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.DisplayName != "" || persisted.Bio != "" || persisted.AvatarURL != "" {
		t.Fatalf("clear patch did not persist empty fields: %#v", persisted)
	}

	ctx, recorder = newUserProfilePatchIntegrationContext(strconvUint(target.ID), `{"bio":"still blocked"}`, other.ID)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("other-user patch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	ctx, recorder = newUserProfilePatchIntegrationContextOptionalViewer(strconvUint(target.ID), `{"bio":"still blocked"}`, nil)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing viewer patch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := db.Delete(&deletedViewer).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = newUserProfilePatchIntegrationContext(strconvUint(target.ID), `{"bio":"still blocked"}`, deletedViewer.ID)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("deleted viewer patch status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	for _, body := range []string{
		`{}`,
		`{"unknown":"value"}`,
		`{"username":"new"}`,
		`{"username":null}`,
		`{"display_name":null}`,
		`{"bio":12}`,
		`{"avatar_url":null}`,
		`{"avatar_url":"/api/files/profile-avatars/` + strconvUint(target.ID) + `/not-a-uuid.jpg"}`,
		`{"avatar_url":"/api/files/profile-avatars/` + strconvUint(other.ID) + `/550e8400-e29b-41d4-a716-446655440000.jpg"}`,
	} {
		ctx, recorder = newUserProfilePatchIntegrationContext(strconvUint(target.ID), body, target.ID)
		UpdateUserProfile(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid patch %s status=%d body=%s", body, recorder.Code, recorder.Body.String())
		}
	}

	overLimitDisplay := `{"display_name":"` + strings.Repeat("界", maxProfileDisplayRunes+1) + `"}`
	overLimitBio := `{"bio":"` + strings.Repeat("界", maxProfileBioRunes+1) + `"}`
	for _, body := range []string{overLimitDisplay, overLimitBio} {
		ctx, recorder = newUserProfilePatchIntegrationContext(strconvUint(target.ID), body, target.ID)
		UpdateUserProfile(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("over-limit patch %s status=%d body=%s", body, recorder.Code, recorder.Body.String())
		}
	}

	ctx, recorder = newUserProfilePatchIntegrationContext("not-a-number", `{"bio":"invalid target"}`, target.ID)
	UpdateUserProfile(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid target patch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func newUserProfilePatchIntegrationContext(targetID, body string, viewerID uint) (*gin.Context, *httptest.ResponseRecorder) {
	return newUserProfilePatchIntegrationContextOptionalViewer(targetID, body, &viewerID)
}

func newUserProfilePatchIntegrationContextOptionalViewer(targetID, body string, viewerID *uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/users/"+targetID, strings.NewReader(body))
	ctx.Params = gin.Params{{Key: "id", Value: targetID}}
	if viewerID != nil {
		ctx.Set("user_id", *viewerID)
	}
	return ctx, recorder
}

func TestUserSearchIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserFollow{}); err != nil {
		t.Fatal(err)
	}
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	token := strings.ReplaceAll(uuid.NewString(), "-", "")
	query := "alice" + token
	viewer := models.User{Username: "search-viewer-" + token, Password: "secret", DisplayName: "Viewer"}
	fixtures := []*models.User{
		&viewer,
		{Username: query, Password: "secret", DisplayName: "not exact"},
		{Username: "display-exact-" + token, Password: "secret", DisplayName: query},
		{Username: query + "-username-prefix", Password: "secret", DisplayName: "prefix"},
		{Username: "display-prefix-" + token, Password: "secret", DisplayName: query + " display"},
		{Username: "contains-" + query + "-user", Password: "secret", DisplayName: "contains"},
		{Username: "display-contains-" + token, Password: "secret", DisplayName: "X " + query + " X"},
		{Username: "deleted-" + query, Password: "secret", DisplayName: query},
		{Username: "literal%" + token, Password: "secret", DisplayName: "literal percent"},
	}
	for _, user := range fixtures {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	deleted := fixtures[7]
	if err := db.Delete(deleted).Error; err != nil {
		t.Fatal(err)
	}
	followed := fixtures[2]
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: followed.ID}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := make([]uint, 0, len(fixtures))
		for _, user := range fixtures {
			ids = append(ids, user.ID)
		}
		db.Where("follower_id = ? OR following_id = ?", viewer.ID, viewer.ID).Delete(&models.UserFollow{})
		db.Unscoped().Where("id IN ?", ids).Delete(&models.User{})
	})

	request := func(q string, limit, offset int) userConnectionPageResponse {
		path := "/api/users/search?q=" + url.QueryEscape(q) + "&limit=" + strconv.Itoa(limit) + "&offset=" + strconv.Itoa(offset)
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
		ctx.Set("user_id", viewer.ID)
		SearchUsers(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("search %q status=%d body=%s", q, recorder.Code, recorder.Body.String())
		}
		var page userConnectionPageResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := request(query, 20, 0)
	expected := []uint{fixtures[1].ID, fixtures[2].ID, fixtures[3].ID, fixtures[4].ID, fixtures[5].ID, fixtures[6].ID}
	if len(page.Items) != len(expected) || page.HasMore {
		t.Fatalf("page=%#v", page)
	}
	for index, id := range expected {
		if page.Items[index].User.ID != id {
			t.Fatalf("rank %d got=%d want=%d", index, page.Items[index].User.ID, id)
		}
	}
	if !page.Items[1].Following || page.Items[0].Following {
		t.Fatalf("following state=%#v", page.Items)
	}
	for _, item := range page.Items {
		if item.User.ID == deleted.ID {
			t.Fatal("soft-deleted search result returned")
		}
	}

	upper := request(strings.ToUpper(query), 20, 0)
	if len(upper.Items) != len(page.Items) {
		t.Fatalf("case insensitive len=%d want=%d", len(upper.Items), len(page.Items))
	}
	atPage := request("@"+query, 20, 0)
	if len(atPage.Items) != len(page.Items) || atPage.Items[0].User.ID != expected[0] {
		t.Fatalf("leading at page=%#v", atPage)
	}
	wildcard := request("%", 20, 0)
	if len(wildcard.Items) == 0 {
		t.Fatal("literal percent did not match controlled user")
	}
	for _, item := range wildcard.Items {
		if !strings.Contains(item.User.Username, "%") && !strings.Contains(item.User.DisplayName, "%") {
			t.Fatalf("wildcard matched unrelated user=%#v", item.User)
		}
	}

	first := request(query, 2, 0)
	second := request(query, 2, 2)
	last := request(query, 2, 4)
	if !first.HasMore || !second.HasMore || last.HasMore || len(first.Items) != 2 || len(second.Items) != 2 || len(last.Items) != 2 {
		t.Fatalf("pagination first=%#v second=%#v last=%#v", first, second, last)
	}
	self := request(viewer.Username, 20, 0)
	if len(self.Items) != 1 || self.Items[0].User.ID != viewer.ID || self.Items[0].Following {
		t.Fatalf("self=%#v", self)
	}
}
