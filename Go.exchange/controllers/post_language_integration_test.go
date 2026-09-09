package controllers

import (
	"encoding/json"
	"net/http"
	"testing"

	"Go.exchange/models"

	"github.com/gin-gonic/gin"
)

func TestNativePostLanguagePersistenceIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	createdIDs := make([]uint, 0, 5)
	t.Cleanup(func() {
		if len(createdIDs) == 0 {
			return
		}
		db.Unscoped().Where("post_id IN ?", createdIDs).Delete(&models.PostReaction{})
		db.Unscoped().Where("post_id IN ?", createdIDs).Delete(&models.PostRepost{})
		db.Unscoped().Where("post_id IN ?", createdIDs).Delete(&models.PostBehavior{})
		db.Unscoped().Where("post_id IN ?", createdIDs).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("post_id IN ?", createdIDs).Delete(&models.PostMedia{})
		if len(createdIDs) > 3 {
			db.Unscoped().Where("id IN ?", createdIDs[3:]).Delete(&models.Post{})
		}
		db.Unscoped().Where("id IN ?", createdIDs[:3]).Delete(&models.Post{})
	})

	create := func(userID uint, content string, replyTo, quote *uint) postResponse {
		t.Helper()
		body, err := json.Marshal(createPostRequest{Content: content, ReplyToPostID: replyTo, QuotePostID: quote})
		if err != nil {
			t.Fatal(err)
		}
		ctx, recorder := newReplyIntegrationContext(http.MethodPost, "/api/posts", "", string(body), userID)
		createPost(ctx, nil)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var response postResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		createdIDs = append(createdIDs, response.ID)
		var stored models.Post
		if err := db.First(&stored, response.ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Language != response.Language {
			t.Fatalf("stored/returned language mismatch stored=%q response=%q", stored.Language, response.Language)
		}
		return response
	}

	chineseRoot := create(fixture.Commenter.ID, "这个推荐系统现在越来越稳定了", nil, nil)
	if chineseRoot.Language != "zh" {
		t.Fatalf("Chinese root response language=%q", chineseRoot.Language)
	}
	japaneseRoot := create(fixture.Other.ID, "今日は新しい推薦システムを試しています", nil, nil)
	if japaneseRoot.Language != "ja" {
		t.Fatalf("Japanese root response language=%q", japaneseRoot.Language)
	}
	englishRoot := create(fixture.Commenter.ID, "The recommendation system is improving steadily today.", nil, nil)
	if englishRoot.Language != "en" {
		t.Fatalf("English root response language=%q", englishRoot.Language)
	}
	chineseReply := create(fixture.Other.ID, "今天准备继续优化内容搜索功能", &englishRoot.ID, nil)
	if chineseReply.Language != "zh" {
		t.Fatalf("Chinese reply response language=%q", chineseReply.Language)
	}
	japaneseQuote := create(fixture.Other.ID, "この機能はとても便利だと思います", nil, &englishRoot.ID)
	if japaneseQuote.Language != "ja" {
		t.Fatalf("Japanese quote response language=%q", japaneseQuote.Language)
	}
}
