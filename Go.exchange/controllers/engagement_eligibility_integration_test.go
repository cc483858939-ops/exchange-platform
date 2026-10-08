package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type engagementEligibilityLogger struct {
	logger.Interface
	queries atomic.Int32
}

func (l *engagementEligibilityLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	if strings.Contains(sql, "post_author") {
		l.queries.Add(1)
	}
}

func TestEngagementEligibilitySharedRequestIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.PostBookmark{}); err != nil {
		t.Fatal(err)
	}
	fixture := newReplyIntegrationFixture(t, db)
	counter := &engagementEligibilityLogger{Interface: logger.Default}
	global.APIDb = db.Session(&gorm.Session{Logger: counter})
	restoreEngagementStateLoaders(t)
	loadPostLikeStates = func(context.Context, uint, []uint) (postLikeStatesLoadResult, error) {
		return postLikeStatesLoadResult{States: map[uint]postLikeStateResult{fixture.Article.ID: {}}}, nil
	}
	loadPostRepostStates = loadPostRepostStatesFromDB
	loadPostBookmarkStates = loadPostBookmarkStatesFromDB

	read := func() engagementStatesResponse {
		t.Helper()
		ctx, recorder := newEngagementStateTestContext(fmt.Sprintf(`{"post_ids":[%d]}`, fixture.Article.ID), &fixture.Commenter.ID)
		GetPostEngagementStates(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var result engagementStatesResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := read()
	if counter.queries.Load() != 1 || len(first.Items) != 1 || first.Items[0].Repost.Status != "ready" || first.Items[0].Bookmark.Status != "ready" {
		t.Fatalf("queries=%d response=%+v", counter.queries.Load(), first)
	}
	if err := db.Delete(&fixture.Author).Error; err != nil {
		t.Fatal(err)
	}
	second := read()
	if counter.queries.Load() != 2 || second.Items[0].Repost.Status != "unavailable" || second.Items[0].Bookmark.Status != "unavailable" {
		t.Fatalf("new request must recheck deleted author: queries=%d response=%+v", counter.queries.Load(), second)
	}
}

func TestEngagementEligibilityKeepsStateFailuresIndependentIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.PostBookmark{}); err != nil {
		t.Fatal(err)
	}
	fixture := newReplyIntegrationFixture(t, db)
	restoreEngagementStateLoaders(t)
	loadPostLikeStates = func(context.Context, uint, []uint) (postLikeStatesLoadResult, error) {
		return postLikeStatesLoadResult{States: map[uint]postLikeStateResult{fixture.Article.ID: {}}}, nil
	}
	loadPostRepostStates = loadPostRepostStatesFromDB
	loadPostBookmarkStates = loadPostBookmarkStatesFromDB
	const callback = "test:engagement_repost_failure"
	if err := db.Callback().Row().Before("gorm:row").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "ar" {
			tx.AddError(errors.New("isolated repost failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Row().Remove(callback) })
	ctx, recorder := newEngagementStateTestContext(fmt.Sprintf(`{"post_ids":[%d]}`, fixture.Article.ID), &fixture.Commenter.ID)
	GetPostEngagementStates(ctx)
	var response engagementStatesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || len(response.Items) != 1 || response.Items[0].Like.Status != "ready" || response.Items[0].Repost.Status != "unavailable" || response.Items[0].Bookmark.Status != "ready" {
		t.Fatalf("state failure leaked: status=%d response=%+v", recorder.Code, response)
	}
}

func TestEngagementEligibilityStandaloneAndCancellationIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.PostBookmark{}); err != nil {
		t.Fatal(err)
	}
	fixture := newReplyIntegrationFixture(t, db)
	counter := &engagementEligibilityLogger{Interface: logger.Default}
	global.APIDb = db.Session(&gorm.Session{Logger: counter})
	ids := []uint{fixture.Article.ID}
	if _, err := loadPostRepostStatesFromDB(context.Background(), fixture.Commenter.ID, ids); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPostBookmarkStatesFromDB(context.Background(), fixture.Commenter.ID, ids); err != nil {
		t.Fatal(err)
	}
	if counter.queries.Load() != 2 {
		t.Fatalf("standalone queries=%d", counter.queries.Load())
	}
	ctx, cancel := context.WithCancel(withSharedPublicPostIDs(context.Background(), ids))
	cancel()
	if _, err := loadPostRepostStatesFromDB(ctx, fixture.Commenter.ID, ids); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled eligibility must fail: %v", err)
	}
	if _, err := loadPostBookmarkStatesFromDB(ctx, fixture.Commenter.ID, ids); !errors.Is(err, context.Canceled) {
		t.Fatalf("shared failure must fail: %v", err)
	}
	ctx = withSharedPublicPostIDs(context.Background(), ids)
	if _, err := loadPublicPostIDs(ctx, global.APIDb, ids, time.Now()); err != nil {
		t.Fatal(err)
	}
	otherIDs := []uint{fixture.Article.ID + 1}
	other, err := loadPublicPostIDs(ctx, global.APIDb, otherIDs, time.Now())
	if err != nil || len(other) != 0 {
		t.Fatalf("different IDs reused the snapshot: ids=%v err=%v", other, err)
	}
}
