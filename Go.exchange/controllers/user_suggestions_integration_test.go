package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestSuggestedUsersQueryIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserFollow{}, &models.Post{}); err != nil {
		t.Fatal(err)
	}
	originalDB := global.APIDb
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	global.APIDb = tx
	t.Cleanup(func() {
		if err := tx.Rollback().Error; err != nil {
			t.Errorf("rollback test transaction: %v", err)
		}
		global.APIDb = originalDB
	})

	token := uuid.NewString()
	makeUser := func(label string) *models.User {
		t.Helper()
		user := &models.User{Username: "suggest-" + label + "-" + token, Password: "secret", DisplayName: label, Bio: "bio-" + label}
		if err := tx.Create(user).Error; err != nil {
			t.Fatal(err)
		}
		return user
	}
	viewer := makeUser("viewer")
	popular := makeUser("popular")
	filler := makeUser("filler")
	authorA := makeUser("author-a")
	authorB := makeUser("author-b")
	authorC := makeUser("author-c")
	alreadyFollowed := makeUser("followed")
	deletedAuthor := makeUser("deleted-author")
	beyondSample := makeUser("beyond-sample")
	oldAuthor := makeUser("old")
	replyAuthor := makeUser("reply")
	deletedPostAuthor := makeUser("deleted-post")
	if err := tx.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: alreadyFollowed.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	postSequence := make([]*models.Post, 0, suggestedUserPostCandidateLimit+6)
	addPostAt := func(author *models.User, minute int, visibility string, replyTo uint, conversationIDs ...uint) *models.Post {
		t.Helper()
		post := &models.Post{
			AuthorID:   author.ID,
			Content:    "candidate",
			Visibility: visibility,
			Model:      gorm.Model{CreatedAt: time.Now().UTC().Add(-time.Duration(minute) * time.Minute)},
		}
		if replyTo > 0 {
			if len(conversationIDs) != 1 || conversationIDs[0] == 0 {
				t.Fatalf("reply post requires one existing conversation root ID")
			}
			conversationID := conversationIDs[0]
			post.ReplyToPostID = &replyTo
			post.ConversationID = &conversationID
		} else if len(conversationIDs) != 0 {
			t.Fatalf("top-level post cannot have a conversation root ID")
		}
		if err := tx.Create(post).Error; err != nil {
			t.Fatal(err)
		}
		postSequence = append(postSequence, post)
		return post
	}

	for minute := 1; minute <= 7; minute++ {
		addPostAt(popular, minute, "public", 0)
	}
	for minute := 8; minute <= 10; minute++ {
		addPostAt(authorA, minute, "public", 0)
	}
	for minute := 11; minute <= 12; minute++ {
		addPostAt(authorB, minute, "public", 0)
	}
	addPostAt(authorC, 13, "public", 0)
	addPostAt(viewer, 14, "public", 0)
	addPostAt(alreadyFollowed, 15, "public", 0)
	addPostAt(deletedAuthor, 16, "public", 0)
	for minute := 17; minute <= 500; minute++ {
		addPostAt(filler, minute, "public", 0)
	}
	for minute := 501; minute <= 506; minute++ {
		addPostAt(beyondSample, minute, "public", 0)
	}
	addPostAt(oldAuthor, 31*24*60, "public", 0)
	addPostAt(replyAuthor, 1, "public", postSequence[0].ID, postSequence[0].ID)
	deletedPost := addPostAt(deletedPostAuthor, 3, "public", 0)
	if err := tx.Delete(deletedPost).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	request := func() userConnectionPageResponse {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/users/suggestions?limit=5", nil)
		ctx.Set("user_id", viewer.ID)
		GetSuggestedUsers(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var page userConnectionPageResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}

	page := request()
	expected := []uint{popular.ID, filler.ID, authorA.ID, authorB.ID, authorC.ID}
	if page.HasMore || len(page.Items) != len(expected) {
		t.Fatalf("page=%#v", page)
	}
	for index, item := range page.Items {
		if item.User.ID != expected[index] || item.Following {
			t.Fatalf("rank %d item=%#v want user=%d and following=false", index, item, expected[index])
		}
	}
	if again := request(); len(again.Items) != len(page.Items) {
		t.Fatalf("second page size=%d first=%d", len(again.Items), len(page.Items))
	} else {
		for index := range page.Items {
			if again.Items[index].User.ID != page.Items[index].User.ID {
				t.Fatalf("unstable ordering: first=%v second=%v", page.Items, again.Items)
			}
		}
	}
}
