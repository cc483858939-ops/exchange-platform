package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func notificationIntegrationContext(method, path string, viewerID uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, nil)
	ctx.Set("user_id", viewerID)
	return ctx, recorder
}

func TestNotificationReadIdempotencyAndIsolationIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Notification{}); err != nil {
		t.Fatal(err)
	}
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	recipient := models.User{Username: "notification-recipient-" + uuid.NewString(), Password: "test"}
	actor := models.User{Username: "notification-actor-" + uuid.NewString(), Password: "test"}
	other := models.User{Username: "notification-other-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&[]*models.User{&recipient, &actor, &other}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	notifications := []models.Notification{
		{RecipientID: recipient.ID, ActorID: actor.ID, Type: models.NotificationTypeUserFollowed, DedupeKey: "test:" + uuid.NewString(), SourceVersion: 1, ActivityAt: now, CreatedAt: now, UpdatedAt: now},
		{RecipientID: other.ID, ActorID: actor.ID, Type: models.NotificationTypeUserFollowed, DedupeKey: "test:" + uuid.NewString(), SourceVersion: 1, ActivityAt: now, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("recipient_id IN ? OR actor_id IN ?", []uint{recipient.ID, actor.ID, other.ID}, []uint{recipient.ID, actor.ID, other.ID}).Delete(&models.Notification{})
		db.Unscoped().Where("id IN ?", []uint{recipient.ID, actor.ID, other.ID}).Delete(&models.User{})
	})

	ctx, recorder := notificationIntegrationContext(http.MethodPut, "/api/me/notifications/"+strconvUint(notifications[0].ID)+"/read", recipient.ID)
	ctx.Params = gin.Params{{Key: "id", Value: strconvUint(notifications[0].ID)}}
	MarkMyNotificationRead(ctx)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("first read status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var first models.Notification
	if err := db.First(&first, notifications[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if first.ReadAt == nil {
		t.Fatal("first read did not set read_at")
	}
	firstReadAt, firstUpdatedAt := *first.ReadAt, first.UpdatedAt

	ctx, recorder = notificationIntegrationContext(http.MethodPut, "/api/me/notifications/"+strconvUint(notifications[0].ID)+"/read", recipient.ID)
	ctx.Params = gin.Params{{Key: "id", Value: strconvUint(notifications[0].ID)}}
	MarkMyNotificationRead(ctx)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("second read status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var second models.Notification
	if err := db.First(&second, notifications[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if second.ReadAt == nil || !second.ReadAt.Equal(firstReadAt) || !second.UpdatedAt.Equal(firstUpdatedAt) {
		t.Fatalf("already-read row changed: before=%s/%s after=%s/%s", firstReadAt, firstUpdatedAt, second.ReadAt, second.UpdatedAt)
	}

	ctx, recorder = notificationIntegrationContext(http.MethodPut, "/api/me/notifications/"+strconvUint(notifications[1].ID)+"/read", recipient.ID)
	ctx.Params = gin.Params{{Key: "id", Value: strconvUint(notifications[1].ID)}}
	MarkMyNotificationRead(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("other user's notification status=%d", recorder.Code)
	}
	ctx, recorder = notificationIntegrationContext(http.MethodPut, "/api/me/notifications/999999999/read", recipient.ID)
	ctx.Params = gin.Params{{Key: "id", Value: "999999999"}}
	MarkMyNotificationRead(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing notification status=%d", recorder.Code)
	}

	bulk := []models.Notification{
		{RecipientID: recipient.ID, ActorID: actor.ID, Type: models.NotificationTypeUserFollowed, DedupeKey: "test:" + uuid.NewString(), SourceVersion: 1, ActivityAt: now.Add(time.Second), CreatedAt: now, UpdatedAt: now},
		{RecipientID: recipient.ID, ActorID: actor.ID, Type: models.NotificationTypeUserFollowed, DedupeKey: "test:" + uuid.NewString(), SourceVersion: 1, ActivityAt: now.Add(2 * time.Second), CreatedAt: now, UpdatedAt: now},
		{RecipientID: other.ID, ActorID: actor.ID, Type: models.NotificationTypeUserFollowed, DedupeKey: "test:" + uuid.NewString(), SourceVersion: 1, ActivityAt: now, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&bulk).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = notificationIntegrationContext(http.MethodPut, "/api/me/notifications/read-all", recipient.ID)
	MarkMyNotificationsReadAll(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("mark-all status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var firstAll struct {
		Updated int64 `json:"updated"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &firstAll); err != nil || firstAll.Updated != 2 {
		t.Fatalf("first mark-all response=%s updated=%d err=%v", recorder.Body.String(), firstAll.Updated, err)
	}
	ctx, recorder = notificationIntegrationContext(http.MethodPut, "/api/me/notifications/read-all", recipient.ID)
	MarkMyNotificationsReadAll(ctx)
	var secondAll struct {
		Updated int64 `json:"updated"`
	}
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &secondAll) != nil || secondAll.Updated != 0 {
		t.Fatalf("second mark-all status=%d body=%s updated=%d", recorder.Code, recorder.Body.String(), secondAll.Updated)
	}
	var otherUnread models.Notification
	if err := db.First(&otherUnread, bulk[2].ID).Error; err != nil {
		t.Fatal(err)
	}
	if otherUnread.ReadAt != nil {
		t.Fatal("mark-all changed another user's notification")
	}
}
