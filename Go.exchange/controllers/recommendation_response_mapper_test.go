package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestGormRecommendationResponseMapperReturnsEmptyWithoutDatabaseAccess(t *testing.T) {
	mapper, err := NewGormRecommendationResponseMapper(&gorm.DB{})
	if err != nil {
		t.Fatal(err)
	}
	responses, err := mapper.Map(context.Background(), nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if responses == nil || len(responses) != 0 {
		t.Fatalf("responses=%#v want a non-nil empty slice", responses)
	}
}

func TestGormRecommendationResponseMapperPropagatesCanceledContext(t *testing.T) {
	mapper, err := NewGormRecommendationResponseMapper(&gorm.DB{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = mapper.Map(ctx, []recommendation.SelectedCandidate{{Post: responseMapperTestPostWithAuthor(7, time.Now().UTC())}}, time.Now().UTC())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context.Canceled", err)
	}
}

func TestGormRecommendationResponseMapperPreservesSelectedOrderAndScoresIntegration(t *testing.T) {
	db := openProfileTimelineIntegrationDB(t)
	author := responseMapperTestAuthor(t, db)
	posts := []models.Post{
		responseMapperTestPostForAuthor(author, "first"),
		responseMapperTestPostForAuthor(author, "second"),
	}
	if err := db.Create(&posts).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("id IN ?", []uint{posts[0].ID, posts[1].ID}).Delete(&models.Post{})
		db.Unscoped().Delete(&models.User{}, author.ID)
	})

	mapper, err := NewGormRecommendationResponseMapper(db)
	if err != nil {
		t.Fatal(err)
	}
	selected := []recommendation.SelectedCandidate{
		{Post: posts[1], Breakdown: recommendation.ScoreBreakdown{FinalScore: 0.72}},
		{Post: posts[0], Breakdown: recommendation.ScoreBreakdown{FinalScore: 0.31}},
	}
	responses, err := mapper.Map(context.Background(), selected, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 2 {
		t.Fatalf("response count=%d want 2", len(responses))
	}
	if responses[0].Post.ID != posts[1].ID || responses[1].Post.ID != posts[0].ID {
		t.Fatalf("response order=%d,%d want %d,%d", responses[0].Post.ID, responses[1].Post.ID, posts[1].ID, posts[0].ID)
	}
	if responses[0].Score != 0.72 || responses[1].Score != 0.31 {
		t.Fatalf("response scores=%v,%v want 0.72,0.31", responses[0].Score, responses[1].Score)
	}
}

func TestGormRecommendationResponseMapperSkipsPostWithoutValidAuthor(t *testing.T) {
	mapper, err := NewGormRecommendationResponseMapper(&gorm.DB{})
	if err != nil {
		t.Fatal(err)
	}
	responses, err := mapper.Map(context.Background(), []recommendation.SelectedCandidate{{Post: responseMapperTestPostWithAuthor(0, time.Now().UTC())}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 0 {
		t.Fatalf("responses=%#v want invalid selected post skipped", responses)
	}
}

func TestGormRecommendationResponseMapperPropagatesDatabaseErrorIntegration(t *testing.T) {
	db := openProfileTimelineIntegrationDB(t)
	mapper, err := NewGormRecommendationResponseMapper(db)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = mapper.Map(context.Background(), []recommendation.SelectedCandidate{{Post: responseMapperTestPostWithAuthor(42, time.Now().UTC())}}, time.Now().UTC())
	if err == nil {
		t.Fatal("expected closed database error")
	}
}

func responseMapperTestPostWithAuthor(authorID uint, now time.Time) models.Post {
	return models.Post{
		Model:      gorm.Model{ID: 42, CreatedAt: now, UpdatedAt: now},
		AuthorID:   authorID,
		Author:     models.User{Model: gorm.Model{ID: authorID}, Username: "mapper-author-" + uuid.NewString()},
		Content:    "mapper test post",
		Language:   "en",
		Visibility: "public",
	}
}

func responseMapperTestAuthor(t *testing.T, db *gorm.DB) models.User {
	t.Helper()
	author := models.User{Username: "mapper-author-" + uuid.NewString(), Password: "secret"}
	if err := db.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	return author
}

func responseMapperTestPostForAuthor(author models.User, content string) models.Post {
	return models.Post{AuthorID: author.ID, Author: author, Content: content, Language: "en", Visibility: "public"}
}
