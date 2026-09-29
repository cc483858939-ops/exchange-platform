package postmediaupload_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/initialize"
	"Go.exchange/models"
	"Go.exchange/postmediaupload"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestClaimCleanupBatchEligibilityReclaimAndTokenCASIntegration(t *testing.T) {
	db, owner := openPostMediaCleanupIntegrationDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	mediaIDs := make([]string, 0, 7)
	t.Cleanup(func() { cleanupPostMediaCleanupFixtures(t, db, mediaIDs, owner.ID) })

	uploadingExpired := createCleanupFixture(t, db, owner.ID, postmediaupload.StatusUploading, now.Add(-4*time.Hour), false)
	uploadedExpired := createCleanupFixture(t, db, owner.ID, postmediaupload.StatusUploaded, now.Add(-3*time.Hour), false)
	pendingExpired := createCleanupFixture(t, db, owner.ID, postmediaupload.StatusCleanupPending, now.Add(-2*time.Hour), false)
	activePending := createCleanupFixture(t, db, owner.ID, postmediaupload.StatusCleanupPending, now.Add(10*time.Minute), true)
	uploadingFuture := createCleanupFixture(t, db, owner.ID, postmediaupload.StatusUploading, now.Add(3*time.Hour), false)
	uploadedFuture := createCleanupFixture(t, db, owner.ID, postmediaupload.StatusUploaded, now.Add(3*time.Hour), false)
	retryFuture := createCleanupFixture(t, db, owner.ID, postmediaupload.StatusCleanupPending, now.Add(time.Hour), false)
	mediaIDs = append(mediaIDs, uploadingExpired.MediaID)
	mediaIDs = append(mediaIDs, uploadedExpired.MediaID)
	mediaIDs = append(mediaIDs, pendingExpired.MediaID)
	mediaIDs = append(mediaIDs, activePending.MediaID)
	mediaIDs = append(mediaIDs, uploadingFuture.MediaID)
	mediaIDs = append(mediaIDs, uploadedFuture.MediaID)
	mediaIDs = append(mediaIDs, retryFuture.MediaID)

	claimTimeout := 15 * time.Minute
	firstClaims, err := postmediaupload.ClaimCleanupBatch(context.Background(), db, now, claimTimeout, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstClaims) != 3 {
		t.Fatalf("first claim count=%d want 3: %+v", len(firstClaims), firstClaims)
	}
	wantFirstIDs := []string{uploadingExpired.MediaID, uploadedExpired.MediaID, pendingExpired.MediaID}
	wantFirstAttempts := map[string]int64{
		uploadingExpired.MediaID: uploadingExpired.CleanupAttempts + 1,
		uploadedExpired.MediaID:  uploadedExpired.CleanupAttempts + 1,
		pendingExpired.MediaID:   pendingExpired.CleanupAttempts + 1,
	}
	firstByID := make(map[string]postmediaupload.CleanupClaim, len(firstClaims))
	for index, claim := range firstClaims {
		if claim.MediaID != wantFirstIDs[index] {
			t.Fatalf("claim order=%v want cleanup_after order=%v", cleanupClaimIDs(firstClaims), wantFirstIDs)
		}
		if claim.ClaimToken == "" || claim.CleanupAttempts != wantFirstAttempts[claim.MediaID] {
			t.Fatalf("claim has invalid token or attempts: %+v", claim)
		}
		firstByID[claim.MediaID] = claim
		if claim.OriginalObjectKey != "gc-fixture/"+claim.MediaID+"/original" || claim.MediumObjectKey != "gc-fixture/"+claim.MediaID+"/medium" || claim.LargeObjectKey != "gc-fixture/"+claim.MediaID+"/large" || claim.ManifestObjectKey != "gc-fixture/"+claim.MediaID+"/manifest" {
			t.Fatalf("claim omitted tracked object keys: %+v", claim)
		}
	}
	for _, future := range []models.PostMediaUpload{activePending, uploadingFuture, uploadedFuture, retryFuture} {
		var stored models.PostMediaUpload
		if err := db.Where("media_id = ?", future.MediaID).Take(&stored).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Status != future.Status || stored.CleanupAttempts != future.CleanupAttempts || !sameOptionalString(stored.CleanupClaimToken, future.CleanupClaimToken) {
			t.Fatalf("future lease was claimed: before=%+v after=%+v", future, stored)
		}
	}

	if claims, err := postmediaupload.ClaimCleanupBatch(context.Background(), db, now, claimTimeout, 10); err != nil || len(claims) != 0 {
		t.Fatalf("active claims were immediately re-claimed: claims=%+v err=%v", claims, err)
	}

	reclaimTime := now.Add(16 * time.Minute)
	reclaimed, err := postmediaupload.ClaimCleanupBatch(context.Background(), db, reclaimTime, claimTimeout, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 4 {
		t.Fatalf("expired active claims were not reclaimed: count=%d claims=%+v", len(reclaimed), reclaimed)
	}
	newByID := make(map[string]postmediaupload.CleanupClaim, len(reclaimed))
	for _, claim := range reclaimed {
		newByID[claim.MediaID] = claim
	}
	for _, oldClaim := range firstClaims {
		newClaim, ok := newByID[oldClaim.MediaID]
		if !ok || newClaim.ClaimToken == oldClaim.ClaimToken || newClaim.CleanupAttempts != oldClaim.CleanupAttempts+1 {
			t.Fatalf("claim was not refreshed with a new token and incremented attempt: old=%+v new=%+v", oldClaim, newClaim)
		}
		if err := postmediaupload.CompleteCleanup(context.Background(), db, oldClaim); !errors.Is(err, postmediaupload.ErrStaleCleanupClaim) {
			t.Fatalf("stale completion error=%v want ErrStaleCleanupClaim", err)
		}
		if err := postmediaupload.ScheduleCleanupRetry(context.Background(), db, oldClaim, reclaimTime.Add(time.Hour), "raw error with secret"); !errors.Is(err, postmediaupload.ErrStaleCleanupClaim) {
			t.Fatalf("stale retry error=%v want ErrStaleCleanupClaim", err)
		}
	}

	previousPendingClaim, ok := firstByID[pendingExpired.MediaID]
	if !ok {
		t.Fatalf("initial pending claim not found for media %q", pendingExpired.MediaID)
	}
	retryClaim, ok := newByID[pendingExpired.MediaID]
	if !ok {
		t.Fatalf("reclaimed pending claim not found for media %q", pendingExpired.MediaID)
	}
	if retryClaim.CleanupAttempts != previousPendingClaim.CleanupAttempts+1 {
		t.Fatalf("reclaimed pending cleanup attempts=%d want %d", retryClaim.CleanupAttempts, previousPendingClaim.CleanupAttempts+1)
	}
	retryAt := reclaimTime.Add(5 * time.Minute)
	longError := strings.Repeat("é", 700)
	if err := postmediaupload.ScheduleCleanupRetry(context.Background(), db, retryClaim, retryAt, longError); err != nil {
		t.Fatal(err)
	}
	var waiting models.PostMediaUpload
	if err := db.Where("media_id = ?", retryClaim.MediaID).Take(&waiting).Error; err != nil {
		t.Fatal(err)
	}
	if waiting.Status != postmediaupload.StatusCleanupPending || waiting.CleanupClaimToken != nil || waiting.CleanupClaimedAt != nil || waiting.CleanupAttempts != retryClaim.CleanupAttempts || !waiting.CleanupAfter.Equal(retryAt) || waiting.LastCleanupError == nil || len(*waiting.LastCleanupError) > postmediaupload.MaxCleanupErrorBytes {
		t.Fatalf("retry state=%+v want attempts=%d", waiting, retryClaim.CleanupAttempts)
	}
	if claims, err := postmediaupload.ClaimCleanupBatch(context.Background(), db, retryAt.Add(-time.Second), claimTimeout, 10); err != nil || len(claims) != 0 {
		t.Fatalf("row was claimable before retry_at: claims=%+v err=%v", claims, err)
	}
	thirdClaims, err := postmediaupload.ClaimCleanupBatch(context.Background(), db, retryAt.Add(time.Second), claimTimeout, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(thirdClaims) != 1 || thirdClaims[0].MediaID != retryClaim.MediaID || thirdClaims[0].ClaimToken == retryClaim.ClaimToken || thirdClaims[0].CleanupAttempts != retryClaim.CleanupAttempts+1 {
		t.Fatalf("retry row was not reclaimed with a fresh token: %+v", thirdClaims)
	}
	if err := postmediaupload.CompleteCleanup(context.Background(), db, retryClaim); !errors.Is(err, postmediaupload.ErrStaleCleanupClaim) {
		t.Fatalf("previous retry claim completion error=%v want ErrStaleCleanupClaim", err)
	}
	if err := postmediaupload.CompleteCleanup(context.Background(), db, thirdClaims[0]); err != nil {
		t.Fatalf("current claim completion: %v", err)
	}
	var gone models.PostMediaUpload
	if err := db.Where("media_id = ?", retryClaim.MediaID).Take(&gone).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("completed cleanup lookup error=%v want row absent", err)
	}
}

func TestClaimCleanupBatchSkipsRowsLockedByCreateIntegration(t *testing.T) {
	db, owner := openPostMediaCleanupIntegrationDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	mediaIDs := []string{}
	t.Cleanup(func() { cleanupPostMediaCleanupFixtures(t, db, mediaIDs, owner.ID) })
	upload := createCleanupFixture(t, db, owner.ID, postmediaupload.StatusUploaded, now.Add(-time.Hour), false)
	mediaIDs = append(mediaIDs, upload.MediaID)

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	locked, err := postmediaupload.LockForConsumption(tx, []string{upload.MediaID})
	if err != nil {
		t.Fatal(err)
	}
	if len(locked) != 1 || locked[0].Status != postmediaupload.StatusUploaded {
		t.Fatalf("create-side lock did not load uploaded lease: %+v", locked)
	}
	claims, err := postmediaupload.ClaimCleanupBatch(context.Background(), db, now, 15*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("GC claimed a row locked by create: %+v", claims)
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	claims, err = postmediaupload.ClaimCleanupBatch(context.Background(), db, now, 15*time.Minute, 10)
	if err != nil || len(claims) != 1 || claims[0].MediaID != upload.MediaID {
		t.Fatalf("unlocked expired row was not claimable: claims=%+v err=%v", claims, err)
	}
}

func openPostMediaCleanupIntegrationDB(t *testing.T) (*gorm.DB, models.User) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := initialize.RunMigrationsWithDB(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	owner := models.User{Username: "post-media-gc-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	return db, owner
}

func createCleanupFixture(t *testing.T, db *gorm.DB, ownerID uint, status string, cleanupAfter time.Time, activeClaim bool) models.PostMediaUpload {
	t.Helper()
	mediaID := uuid.NewString()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	upload := models.PostMediaUpload{
		MediaID:           mediaID,
		OwnerID:           ownerID,
		Status:            postmediaupload.StatusUploading,
		OriginalObjectKey: "gc-fixture/" + mediaID + "/original",
		MediumObjectKey:   "gc-fixture/" + mediaID + "/medium",
		LargeObjectKey:    "gc-fixture/" + mediaID + "/large",
		ManifestObjectKey: "gc-fixture/" + mediaID + "/manifest",
		MediumURL:         "/gc-fixture/" + mediaID + "/medium",
		LargeURL:          "/gc-fixture/" + mediaID + "/large",
		Width:             1200,
		Height:            800,
		CreatedAt:         createdAt,
		CleanupAfter:      cleanupAfter,
	}
	if err := postmediaupload.CreateUploading(context.Background(), db, upload); err != nil {
		t.Fatalf("create upload fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Unscoped().Where("media_id = ?", mediaID).Delete(&models.PostMediaUpload{}).Error; err != nil {
			t.Errorf("clean up upload fixture %s: %v", mediaID, err)
		}
	})
	if status != postmediaupload.StatusUploading {
		uploadedAt := cleanupAfter.Add(-time.Hour)
		if err := postmediaupload.MarkUploaded(context.Background(), db, mediaID, ownerID, uploadedAt, cleanupAfter); err != nil {
			t.Fatalf("mark upload fixture uploaded: %v", err)
		}
		upload.Status = postmediaupload.StatusUploaded
		upload.UploadedAt = &uploadedAt
		upload.CleanupAfter = cleanupAfter
	}
	if status == postmediaupload.StatusCleanupPending {
		updates := map[string]interface{}{"status": postmediaupload.StatusCleanupPending, "cleanup_attempts": int64(1)}
		if activeClaim {
			token := uuid.NewString()
			claimedAt := time.Now().UTC().Truncate(time.Microsecond)
			updates["cleanup_claim_token"] = token
			updates["cleanup_claimed_at"] = claimedAt
			upload.CleanupClaimToken = &token
			upload.CleanupClaimedAt = &claimedAt
		}
		if err := db.Model(&models.PostMediaUpload{}).Where("media_id = ?", mediaID).Updates(updates).Error; err != nil {
			t.Fatalf("create cleanup_pending fixture: %v", err)
		}
		upload.Status = postmediaupload.StatusCleanupPending
		upload.CleanupAttempts = 1
	}
	var stored models.PostMediaUpload
	if err := db.Where("media_id = ?", mediaID).Take(&stored).Error; err != nil {
		t.Fatalf("load committed cleanup fixture %q: %v", mediaID, err)
	}
	if stored.Status != upload.Status || !sameOptionalTime(stored.UploadedAt, upload.UploadedAt) || !stored.CleanupAfter.Equal(upload.CleanupAfter) || !sameOptionalString(stored.CleanupClaimToken, upload.CleanupClaimToken) || !sameOptionalTime(stored.CleanupClaimedAt, upload.CleanupClaimedAt) || stored.CleanupAttempts != upload.CleanupAttempts {
		t.Fatalf("cleanup fixture does not match committed row: fixture=%+v stored=%+v", upload, stored)
	}
	return upload
}

func cleanupPostMediaCleanupFixtures(t *testing.T, db *gorm.DB, mediaIDs []string, ownerID uint) {
	t.Helper()
	if len(mediaIDs) > 0 {
		if err := db.Unscoped().Where("media_id IN ?", mediaIDs).Delete(&models.PostMediaUpload{}).Error; err != nil {
			t.Errorf("clean up Post media GC rows: %v", err)
		}
	}
	if err := db.Unscoped().Delete(&models.User{}, ownerID).Error; err != nil {
		t.Errorf("clean up Post media GC owner: %v", err)
	}
}

func cleanupClaimIDs(claims []postmediaupload.CleanupClaim) []string {
	ids := make([]string, len(claims))
	for index, claim := range claims {
		ids[index] = claim.MediaID
	}
	return ids
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
