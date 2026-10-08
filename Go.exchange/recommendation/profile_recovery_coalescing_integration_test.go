package recommendation

import (
	"Go.exchange/models"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestProfileRecoveryCoalescesSuccessButRetriesFailuresAndInvalidatesIntegration(t *testing.T) {
	db := openRecommendationProfileControllerIntegrationDB(t)
	user := newRecommendationProfileControllerIntegrationUser(t, db, "coalescing")
	repository, err := NewGormProfileRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("recovery_count_%d", time.Now().UnixNano())
	writes := 0
	rejectWrite := false
	cause := errors.New("transient enqueue failure")
	if err := db.Callback().Create().Before("gorm:create").Register(name, func(query *gorm.DB) {
		if query.Statement.Table == "user_reco_profile_dirty" {
			writes++
			if rejectWrite {
				query.AddError(cause)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Create().Remove(name) })
	now := time.Now().UTC()
	for i := 0; i < 100; i++ {
		result := repository.withRecovery(t.Context(), Profile{ProfileStatus: ProfileStatusMiss}, user.ID, "serving_miss", now)
		if result.RecoveryError != nil {
			t.Fatal(result.RecoveryError)
		}
	}
	if writes != 1 {
		t.Fatalf("100 repeated fallbacks wrote %d enqueues, want 1", writes)
	}
	dirty, _ := NewGormDirtyProfileRepository(db)
	for i := 0; i < 3; i++ {
		if err := dirty.InvalidateProfiles(t.Context(), []uint{user.ID}, "behavior", now); err != nil {
			t.Fatal(err)
		}
	}
	row, err := dirty.Load(t.Context(), user.ID)
	if err != nil || row.DirtyVersion != 4 {
		t.Fatalf("behavior invalidation lost: %+v err=%v", row, err)
	}
	repository.recoveryMu.Lock()
	repository.recoveryQueued[user.ID] = time.Now().Add(-time.Second)
	repository.recoveryMu.Unlock()
	before := writes
	rejectWrite = true
	result := repository.withRecovery(t.Context(), Profile{}, user.ID, "serving_miss", now)
	if !errors.Is(result.RecoveryError, cause) {
		t.Fatalf("failure=%v", result.RecoveryError)
	}
	rejectWrite = false
	result = repository.withRecovery(t.Context(), Profile{}, user.ID, "serving_miss", now)
	if result.RecoveryError != nil || writes != before+2 {
		t.Fatalf("failed enqueue suppressed retry: writes=%d err=%v", writes, result.RecoveryError)
	}
	// A valid profile read removes the short recovery hint immediately.
	profile := models.UserRecoProfile{UserID: user.ID, ProfileVersion: MaterializedProfileVersion, ProfileConfigHash: "coalescing", EmbeddingVersion: "coalescing", ComputedAt: now, NextRebuildAt: now.Add(time.Hour)}
	if err := db.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Load(t.Context(), ProfileLoadQuery{UserID: user.ID, ExpectedProfileVersion: MaterializedProfileVersion, ExpectedProfileConfigHash: "coalescing", EmbeddingVersion: "coalescing", Now: now}); err != nil {
		t.Fatal(err)
	}
	repository.recoveryMu.Lock()
	_, cached := repository.recoveryQueued[user.ID]
	repository.recoveryMu.Unlock()
	if cached {
		t.Fatal("profile hit retained recovery suppression")
	}
}
