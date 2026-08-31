package controllers

import (
	"testing"

	"Go.exchange/models"
)

func TestCreateReplyWithCountPersistsReplyBehaviorIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.PostBehavior{}); err != nil {
		t.Fatal(err)
	}
	fixture := newReplyIntegrationFixture(t, db)

	first, err := createReplyWithCount(fixture.Article.ID, fixture.Commenter.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := createReplyWithCount(fixture.Article.ID, fixture.Commenter.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err := deleteReplyWithCount(first.ID, fixture.Commenter.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteReplyWithCount(second.ID, fixture.Commenter.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&behavior, behavior.ID).Error; err != nil {
		t.Fatal(err)
	}
	if behavior.Count != 2 || !behavior.Active {
		t.Fatalf("reply behavior after delete=%#v", behavior)
	}
}
