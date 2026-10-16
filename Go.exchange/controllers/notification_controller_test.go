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
	if err := db.AutoMigrate(&models.User{}, &models.UserMute{}, &models.Notification{}); err != nil {
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
	if err := db.AutoMigrate(&models.User{}, &models.UserMute{}, &models.Post{}, &models.Notification{}); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	for _, statement := range []string{
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_type",
		"ALTER TABLE notifications ADD CONSTRAINT chk_notifications_type CHECK (notification_type IN ('post_liked', 'post_replied', 'post_quoted', 'user_followed', 'post_mentioned'))",
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_shape",
		`ALTER TABLE notifications ADD CONSTRAINT chk_notifications_shape CHECK (
  (notification_type = 'post_liked' AND post_id IS NOT NULL AND source_version > 0) OR
  (notification_type = 'post_replied' AND post_id IS NOT NULL AND source_version = 0) OR
  (notification_type = 'post_quoted' AND post_id IS NOT NULL AND source_version = 0) OR
  (notification_type = 'post_mentioned' AND post_id IS NOT NULL AND source_version = 0) OR
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

func TestMentionNotificationVisibilityAndReadStateIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserMute{}, &models.Post{}, &models.Notification{}); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	originalDB := global.APIDb
	t.Cleanup(func() {
		if err := tx.Rollback().Error; err != nil {
			t.Errorf("rollback mention notification integration transaction: %v", err)
		}
		global.APIDb = originalDB
	})
	for _, statement := range []string{
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_type",
		"ALTER TABLE notifications ADD CONSTRAINT chk_notifications_type CHECK (notification_type IN ('post_liked', 'post_replied', 'post_quoted', 'user_followed', 'post_mentioned'))",
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_shape",
		`ALTER TABLE notifications ADD CONSTRAINT chk_notifications_shape CHECK (
  (notification_type = 'post_liked' AND post_id IS NOT NULL AND source_version > 0) OR
  (notification_type = 'post_replied' AND post_id IS NOT NULL AND source_version = 0) OR
  (notification_type = 'post_quoted' AND post_id IS NOT NULL AND source_version = 0) OR
  (notification_type = 'post_mentioned' AND post_id IS NOT NULL AND source_version = 0) OR
  (notification_type = 'user_followed' AND post_id IS NULL AND source_version > 0)
)`,
	} {
		if err := tx.Exec(statement).Error; err != nil {
			t.Fatalf("prepare mention notification schema: %v", err)
		}
	}
	global.APIDb = tx

	recipient := models.User{Username: "mention-notification-recipient-" + uuid.NewString(), Password: "test"}
	actor := models.User{Username: "mention-notification-actor-" + uuid.NewString(), Password: "test"}
	if err := tx.Create(&[]*models.User{&recipient, &actor}).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{AuthorID: actor.ID, Content: "hello @" + recipient.Username, Visibility: "public"}
	if err := tx.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	postID := post.ID
	notification := models.Notification{
		RecipientID: recipient.ID, ActorID: actor.ID, Type: models.NotificationTypePostMentioned,
		PostID: &postID, DedupeKey: "post_mentioned:" + strconvUint(post.ID) + ":" + strconvUint(recipient.ID),
		SourceVersion: 0, ActivityAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Create(&notification).Error; err != nil {
		t.Fatal(err)
	}

	ctx, recorder := notificationIntegrationContext(http.MethodGet, "/api/me/notifications", recipient.ID)
	GetMyNotifications(ctx)
	var page notificationPageResponse
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Type != models.NotificationTypePostMentioned {
		t.Fatalf("mention list status=%d body=%s page=%+v", recorder.Code, recorder.Body.String(), page)
	}
	readUnread := func() int64 {
		t.Helper()
		ctx, recorder := notificationIntegrationContext(http.MethodGet, "/api/me/notifications/unread-count", recipient.ID)
		GetMyUnreadNotificationCount(ctx)
		var count struct {
			UnreadCount int64 `json:"unread_count"`
		}
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &count) != nil {
			t.Fatalf("unread status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		return count.UnreadCount
	}
	if count := readUnread(); count != 1 {
		t.Fatalf("mention unread count=%d want=1", count)
	}
	ctx, recorder = notificationIntegrationContext(http.MethodPut, "/api/me/notifications/"+strconvUint(notification.ID)+"/read", recipient.ID)
	ctx.Params = gin.Params{{Key: "id", Value: strconvUint(notification.ID)}}
	MarkMyNotificationRead(ctx)
	if ctx.Writer.Status() != http.StatusNoContent || readUnread() != 0 {
		t.Fatalf("mark mention read status=%d unread=%d", ctx.Writer.Status(), readUnread())
	}
	if err := tx.Create(&models.UserMute{MuterID: recipient.ID, MutedID: actor.ID}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = notificationIntegrationContext(http.MethodGet, "/api/me/notifications", recipient.ID)
	GetMyNotifications(ctx)
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &page) != nil || len(page.Items) != 0 || readUnread() != 0 {
		t.Fatalf("muted mention status=%d body=%s page=%+v", recorder.Code, recorder.Body.String(), page)
	}
	if err := tx.Where("muter_id = ? AND muted_id = ?", recipient.ID, actor.ID).Delete(&models.UserMute{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(&post).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = notificationIntegrationContext(http.MethodGet, "/api/me/notifications", recipient.ID)
	GetMyNotifications(ctx)
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &page) != nil || len(page.Items) != 0 || readUnread() != 0 {
		t.Fatalf("deleted-post mention status=%d body=%s page=%+v", recorder.Code, recorder.Body.String(), page)
	}
}

func TestMutedActorNotificationsFilterListAndUnreadCountIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserMute{}, &models.Notification{}); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	originalDB := global.APIDb
	global.APIDb = tx
	t.Cleanup(func() {
		if err := tx.Rollback().Error; err != nil {
			t.Errorf("rollback muted notification integration transaction: %v", err)
		}
		global.APIDb = originalDB
	})

	newUser := func(name string) models.User {
		t.Helper()
		user := models.User{Username: "muted-notification-" + name + "-" + uuid.NewString(), Password: "test"}
		if err := tx.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		return user
	}
	viewer, otherViewer := newUser("viewer"), newUser("other-viewer")
	mutedActor, visibleActor := newUser("muted-actor"), newUser("visible-actor")
	now := time.Now().UTC().Truncate(time.Microsecond)
	readAt := now
	notifications := []models.Notification{
		{RecipientID: viewer.ID, ActorID: mutedActor.ID, Type: models.NotificationTypeUserFollowed, DedupeKey: "muted-read:" + uuid.NewString(), SourceVersion: 1, ActivityAt: now, ReadAt: &readAt, CreatedAt: now, UpdatedAt: now},
		{RecipientID: viewer.ID, ActorID: mutedActor.ID, Type: models.NotificationTypeUserFollowed, DedupeKey: "muted-unread:" + uuid.NewString(), SourceVersion: 1, ActivityAt: now.Add(time.Second), CreatedAt: now, UpdatedAt: now},
		{RecipientID: viewer.ID, ActorID: visibleActor.ID, Type: models.NotificationTypeUserFollowed, DedupeKey: "visible:" + uuid.NewString(), SourceVersion: 1, ActivityAt: now.Add(2 * time.Second), CreatedAt: now, UpdatedAt: now},
		{RecipientID: otherViewer.ID, ActorID: mutedActor.ID, Type: models.NotificationTypeUserFollowed, DedupeKey: "other-viewer:" + uuid.NewString(), SourceVersion: 1, ActivityAt: now, CreatedAt: now, UpdatedAt: now},
	}
	if err := tx.Create(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Create(&models.UserMute{MuterID: viewer.ID, MutedID: mutedActor.ID}).Error; err != nil {
		t.Fatal(err)
	}

	readPage := func(recipientID uint) notificationPageResponse {
		t.Helper()
		ctx, recorder := notificationIntegrationContext(http.MethodGet, "/api/me/notifications", recipientID)
		GetMyNotifications(ctx)
		var page notificationPageResponse
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &page) != nil {
			t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		return page
	}
	readUnread := func(recipientID uint) int64 {
		t.Helper()
		ctx, recorder := notificationIntegrationContext(http.MethodGet, "/api/me/notifications/unread-count", recipientID)
		GetMyUnreadNotificationCount(ctx)
		var count struct {
			UnreadCount int64 `json:"unread_count"`
		}
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &count) != nil {
			t.Fatalf("unread status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		return count.UnreadCount
	}

	mutedPage := readPage(viewer.ID)
	mutedUnread := readUnread(viewer.ID)
	if len(mutedPage.Items) != 1 || mutedPage.Items[0].Actor.ID != visibleActor.ID || mutedUnread != 1 {
		t.Fatalf("muted list=%#v unread=%d, want visible actor only and count 1", mutedPage.Items, mutedUnread)
	}
	otherPage := readPage(otherViewer.ID)
	if len(otherPage.Items) != 1 || otherPage.Items[0].Actor.ID != mutedActor.ID {
		t.Fatalf("other viewer inherited filter: %#v", otherPage.Items)
	}
	if err := tx.Where("muter_id = ? AND muted_id = ?", viewer.ID, mutedActor.ID).Delete(&models.UserMute{}).Error; err != nil {
		t.Fatal(err)
	}
	unmutedPage := readPage(viewer.ID)
	unmutedUnread := readUnread(viewer.ID)
	if len(unmutedPage.Items) != 3 || unmutedUnread != 2 {
		t.Fatalf("unmuted list=%#v unread=%d, want all viewer notifications and count 2", unmutedPage.Items, unmutedUnread)
	}
}
