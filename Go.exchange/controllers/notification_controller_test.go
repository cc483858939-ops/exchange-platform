package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Notification{}); err != nil {
		t.Fatal(err)
	}
	originalDB := global.APIDb
	global.APIDb = db
	t.Cleanup(func() { global.APIDb = originalDB })

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
	if ctx.Writer.Status() != http.StatusNoContent {
		t.Fatalf("first read status=%d body=%s", ctx.Writer.Status(), recorder.Body.String())
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
	if ctx.Writer.Status() != http.StatusNoContent {
		t.Fatalf("second read status=%d body=%s", ctx.Writer.Status(), recorder.Body.String())
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

func TestQuotedNotificationVisibilityTracksQuotePostIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.Notification{}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_type",
		"ALTER TABLE notifications ADD CONSTRAINT chk_notifications_type CHECK (notification_type IN ('post_liked', 'post_replied', 'post_quoted', 'user_followed'))",
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_shape",
		`ALTER TABLE notifications ADD CONSTRAINT chk_notifications_shape CHECK (
  (notification_type = 'post_liked' AND post_id IS NOT NULL AND source_version > 0) OR
  (notification_type = 'post_replied' AND post_id IS NOT NULL AND source_version = 0) OR
  (notification_type = 'post_quoted' AND post_id IS NOT NULL AND source_version = 0) OR
  (notification_type = 'user_followed' AND post_id IS NULL AND source_version > 0)
)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("prepare quoted notification schema: %v", err)
		}
	}
	originalDB := global.APIDb
	global.APIDb = db
	t.Cleanup(func() { global.APIDb = originalDB })

	recipient := models.User{Username: "quoted-notification-recipient-" + uuid.NewString(), Password: "test"}
	actor := models.User{Username: "quoted-notification-actor-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&[]*models.User{&recipient, &actor}).Error; err != nil {
		t.Fatal(err)
	}
	target := models.Post{AuthorID: recipient.ID, Content: "original target", Visibility: "public"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	quote := models.Post{AuthorID: actor.ID, Content: "quote post", Visibility: "public", QuotePostID: &target.ID}
	if err := db.Create(&quote).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	quoteID := quote.ID
	notification := models.Notification{
		RecipientID: recipient.ID, ActorID: actor.ID, Type: models.NotificationTypePostQuoted,
		PostID: &quoteID, DedupeKey: "post_quote:" + strconvUint(quote.ID), SourceVersion: 0,
		ActivityAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&notification).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("recipient_id IN ? OR actor_id IN ?", []uint{recipient.ID, actor.ID}, []uint{recipient.ID, actor.ID}).Delete(&models.Notification{})
		db.Unscoped().Where("id = ?", quote.ID).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", target.ID).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{recipient.ID, actor.ID}).Delete(&models.User{})
	})

	ctx, recorder := notificationIntegrationContext(http.MethodGet, "/api/me/notifications", recipient.ID)
	GetMyNotifications(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page notificationPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Type != models.NotificationTypePostQuoted || page.Items[0].PostID == nil || *page.Items[0].PostID != quote.ID {
		t.Fatalf("quoted notification page=%+v", page)
	}
	ctx, recorder = notificationIntegrationContext(http.MethodGet, "/api/me/notifications/unread-count", recipient.ID)
	GetMyUnreadNotificationCount(ctx)
	var count struct {
		UnreadCount int64 `json:"unread_count"`
	}
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &count) != nil || count.UnreadCount != 1 {
		t.Fatalf("unread status=%d body=%s decoded=%d", recorder.Code, recorder.Body.String(), count.UnreadCount)
	}

	if err := db.Delete(&quote).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = notificationIntegrationContext(http.MethodGet, "/api/me/notifications", recipient.ID)
	GetMyNotifications(ctx)
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &page) != nil || len(page.Items) != 0 {
		t.Fatalf("list after quote deletion status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	ctx, recorder = notificationIntegrationContext(http.MethodGet, "/api/me/notifications/unread-count", recipient.ID)
	GetMyUnreadNotificationCount(ctx)
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &count) != nil || count.UnreadCount != 0 {
		t.Fatalf("unread after quote deletion status=%d body=%s decoded=%d", recorder.Code, recorder.Body.String(), count.UnreadCount)
	}
}
