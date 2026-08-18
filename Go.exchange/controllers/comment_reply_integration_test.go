package controllers

import (
	"testing"

	"Go.exchange/models"
)

func TestCreateCommentWithCountPersistsReplyBehaviorIntegration(t *testing.T) {
	db := openCommentIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.ArticleBehavior{}); err != nil {
		t.Fatal(err)
	}
	fixture := newCommentIntegrationFixture(t, db)

	first, err := createCommentWithCount(fixture.Article.ID, fixture.Commenter.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := createCommentWithCount(fixture.Article.ID, fixture.Commenter.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	var behavior models.ArticleBehavior
	if err := db.Where("user_id = ? AND article_id = ? AND action = ?", fixture.Commenter.ID, fixture.Article.ID, ArticleBehaviorActionReply).First(&behavior).Error; err != nil {
		t.Fatal(err)
	}
	if behavior.Count != 2 || !behavior.Active {
		t.Fatalf("reply behavior=%#v", behavior)
	}
	var article models.Article
	if err := db.First(&article, fixture.Article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if article.CommentCount != 2 {
		t.Fatalf("comment count=%d", article.CommentCount)
	}
	if _, err := deleteCommentWithCount(first.ID, fixture.Commenter.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteCommentWithCount(second.ID, fixture.Commenter.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&behavior, behavior.ID).Error; err != nil {
		t.Fatal(err)
	}
	if behavior.Count != 2 || !behavior.Active {
		t.Fatalf("reply behavior after delete=%#v", behavior)
	}
}
