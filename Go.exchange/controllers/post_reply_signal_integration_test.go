package controllers

import (
	"encoding/json"
	"net/http"
	"testing"

	"Go.exchange/models"
)

func TestCanonicalReplyPersistsReplyBehaviorIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.PostBehavior{}); err != nil {
		t.Fatal(err)
	}
	fixture := newReplyIntegrationFixture(t, db)

	createReply := func(content string) uint {
		ctx, recorder := newReplyIntegrationContext(
			http.MethodPost, "/api/posts", strconvUint(fixture.Article.ID),
			`{"content":"`+content+`","reply_to_post_id":`+strconvUint(fixture.Article.ID)+`}`,
			fixture.Commenter.ID,
		)
		createPost(ctx, nil)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var response replyResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.ID
	}
	first := createReply("first")
	second := createReply("second")
	var behavior models.PostBehavior
	if err := db.Where("user_id = ? AND post_id = ? AND action = ?", fixture.Commenter.ID, fixture.Article.ID, PostBehaviorActionReply).First(&behavior).Error; err != nil {
		t.Fatal(err)
	}
	if behavior.Count != 2 || !behavior.Active {
		t.Fatalf("reply behavior=%#v", behavior)
	}
	var article models.Post
	if err := db.First(&article, fixture.Article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if article.ReplyCount != 2 {
		t.Fatalf("comment count=%d", article.ReplyCount)
	}
	deleteReply := func(replyID uint) {
		ctx, recorder := newReplyIntegrationContext(http.MethodDelete, "/api/posts/"+strconvUint(replyID), strconvUint(replyID), "", fixture.Commenter.ID)
		DeletePost(ctx)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	deleteReply(first)
	deleteReply(second)
	if err := db.First(&behavior, behavior.ID).Error; err != nil {
		t.Fatal(err)
	}
	if behavior.Count != 2 || !behavior.Active {
		t.Fatalf("reply behavior after delete=%#v", behavior)
	}
}
