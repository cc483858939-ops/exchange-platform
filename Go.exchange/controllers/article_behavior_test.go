package controllers

import (
	"errors"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestRecordArticleBehaviorFromContextSkipsWithoutUsername(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalRecordArticleBehavior := recordArticleBehavior
	originalArticleBehaviorLogError := articleBehaviorLogError
	defer func() {
		recordArticleBehavior = originalRecordArticleBehavior
		articleBehaviorLogError = originalArticleBehaviorLogError
	}()

	called := false
	recordArticleBehavior = func(string, uint, string) error {
		called = true
		return nil
	}
	articleBehaviorLogError = func(*gin.Context, string, error) {}

	ctx, _ := gin.CreateTestContext(nil)
	recordArticleBehaviorFromContext(ctx, 7, ArticleBehaviorActionView)

	if called {
		t.Fatal("expected behavior recording to be skipped without username")
	}
}

func TestRecordArticleBehaviorFromContextRecordsTrimmedUsername(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalRecordArticleBehavior := recordArticleBehavior
	originalArticleBehaviorLogError := articleBehaviorLogError
	defer func() {
		recordArticleBehavior = originalRecordArticleBehavior
		articleBehaviorLogError = originalArticleBehaviorLogError
	}()

	recordedUsername := ""
	recordedArticleID := uint(0)
	recordedAction := ""
	recordArticleBehavior = func(username string, articleID uint, action string) error {
		recordedUsername = username
		recordedArticleID = articleID
		recordedAction = action
		return nil
	}
	articleBehaviorLogError = func(*gin.Context, string, error) {}

	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set("username", " alice ")

	recordArticleBehaviorFromContext(ctx, 9, ArticleBehaviorActionLike)

	if recordedUsername != "alice" || recordedArticleID != 9 || recordedAction != ArticleBehaviorActionLike {
		t.Fatalf("unexpected recorded behavior: username=%q articleID=%d action=%q", recordedUsername, recordedArticleID, recordedAction)
	}
}

func TestRecordArticleBehaviorFromContextLogsRecordErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalRecordArticleBehavior := recordArticleBehavior
	originalArticleBehaviorLogError := articleBehaviorLogError
	defer func() {
		recordArticleBehavior = originalRecordArticleBehavior
		articleBehaviorLogError = originalArticleBehaviorLogError
	}()

	expectedErr := errors.New("db down")
	recordArticleBehavior = func(string, uint, string) error {
		return expectedErr
	}

	logged := false
	articleBehaviorLogError = func(_ *gin.Context, _ string, err error) {
		logged = errors.Is(err, expectedErr)
	}

	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set("username", "alice")

	recordArticleBehaviorFromContext(ctx, 9, ArticleBehaviorActionLike)

	if !logged {
		t.Fatal("expected record error to be logged")
	}
}

func TestArticleBehaviorIDsBeyondRetentionKeepsLatestRecords(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	behaviors := make([]models.ArticleBehavior, 0, articleBehaviorRetentionLimit+5)
	for id := uint(1); id <= articleBehaviorRetentionLimit+5; id++ {
		behaviors = append(behaviors, models.ArticleBehavior{
			Model:      gorm.Model{ID: id},
			LastSeenAt: now.Add(time.Duration(id) * time.Second),
			Active:     true,
		})
	}

	archiveIDs := articleBehaviorIDsBeyondRetention(behaviors, articleBehaviorRetentionLimit)
	if len(archiveIDs) != 5 {
		t.Fatalf("expected 5 archived ids, got %d: %v", len(archiveIDs), archiveIDs)
	}

	expectedOldIDs := map[uint]struct{}{1: {}, 2: {}, 3: {}, 4: {}, 5: {}}
	for _, id := range archiveIDs {
		if _, ok := expectedOldIDs[id]; !ok {
			t.Fatalf("expected only oldest ids to be archived, got id=%d all=%v", id, archiveIDs)
		}
	}
}

func TestArticleBehaviorIDsBeyondRetentionUsesIDTieBreaker(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	behaviors := []models.ArticleBehavior{
		{Model: gorm.Model{ID: 1}, LastSeenAt: now, Active: true},
		{Model: gorm.Model{ID: 2}, LastSeenAt: now, Active: true},
		{Model: gorm.Model{ID: 3}, LastSeenAt: now, Active: true},
	}

	archiveIDs := articleBehaviorIDsBeyondRetention(behaviors, 2)
	if len(archiveIDs) != 1 || archiveIDs[0] != 1 {
		t.Fatalf("expected the lowest id to be archived on timestamp tie, got %v", archiveIDs)
	}
}
