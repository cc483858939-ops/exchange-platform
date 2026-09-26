package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newPostDeleteContext(id string, viewerID *uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/api/posts/"+id, nil)
	ctx.Params = gin.Params{{Key: "id", Value: id}}
	if viewerID != nil {
		ctx.Set("user_id", *viewerID)
	}
	return ctx, recorder
}

func stubPostDeleteDependencies(t *testing.T, transactionErr error) {
	t.Helper()
	originalViewer := loadPostDeleteViewer
	originalTransaction := deletePostInTransaction
	originalDetail := invalidatePostDeleteDetailCache
	originalLikes := cleanupDeletedPostLikeState
	t.Cleanup(func() {
		loadPostDeleteViewer = originalViewer
		deletePostInTransaction = originalTransaction
		invalidatePostDeleteDetailCache = originalDetail
		cleanupDeletedPostLikeState = originalLikes
	})

	loadPostDeleteViewer = func(context.Context, uint) error { return nil }
	deletePostInTransaction = func(context.Context, uint, uint) (postDeleteResult, error) {
		return postDeleteResult{}, transactionErr
	}
	invalidatePostDeleteDetailCache = func(uint) error { return nil }
	cleanupDeletedPostLikeState = func(uint) error { return nil }
}

func TestDeletePostRejectsInvalidID(t *testing.T) {
	for _, id := range []string{"0", "-1", "abc"} {
		ctx, recorder := newPostDeleteContext(id, nil)
		DeletePost(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("id=%q status=%d body=%s", id, recorder.Code, recorder.Body.String())
		}
	}
}

func TestDeletePostRejectsMissingAuthContext(t *testing.T) {
	ctx, recorder := newPostDeleteContext("42", nil)
	DeletePost(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeletePostRejectsMissingOrInactiveViewer(t *testing.T) {
	originalViewer := loadPostDeleteViewer
	t.Cleanup(func() { loadPostDeleteViewer = originalViewer })
	loadPostDeleteViewer = func(context.Context, uint) error { return gorm.ErrRecordNotFound }

	viewerID := uint(7)
	ctx, recorder := newPostDeleteContext("42", &viewerID)
	DeletePost(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeletePostMapsMissingAndForbiddenTransactions(t *testing.T) {
	viewerID := uint(7)
	for _, testCase := range []struct {
		name string
		err  error
		want int
	}{
		{name: "missing", err: errPostDeleteNotFound, want: http.StatusNotFound},
		{name: "forbidden", err: errPostDeleteForbidden, want: http.StatusForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stubPostDeleteDependencies(t, testCase.err)
			ctx, recorder := newPostDeleteContext("42", &viewerID)
			DeletePost(ctx)
			if recorder.Code != testCase.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if testCase.want == http.StatusNotFound && recorder.Body.String() != "{\"error\":\"post not found\"}" {
				t.Fatalf("body=%s", recorder.Body.String())
			}
			if testCase.want == http.StatusForbidden && recorder.Body.String() != "{\"error\":\"forbidden\"}" {
				t.Fatalf("body=%s", recorder.Body.String())
			}
		})
	}
}

func TestDeletePostOwnerReturnsNoContentAndCleansUpOnce(t *testing.T) {
	viewerID := uint(7)
	var detailCalls, likeCalls int
	stubPostDeleteDependencies(t, nil)
	invalidatePostDeleteDetailCache = func(uint) error {
		detailCalls++
		return errors.New("detail cache unavailable")
	}
	cleanupDeletedPostLikeState = func(uint) error {
		likeCalls++
		return errors.New("redis unavailable")
	}

	ctx, recorder := newPostDeleteContext("42", &viewerID)
	DeletePost(ctx)
	ctx.Writer.WriteHeaderNow()
	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if detailCalls != 1 || likeCalls != 1 {
		t.Fatalf("cleanup calls detail=%d likes=%d", detailCalls, likeCalls)
	}
}

func TestDeletePostInvalidatesDeletedPostAndDirectParent(t *testing.T) {
	viewerID := uint(7)
	stubPostDeleteDependencies(t, nil)
	parentID := uint(9)
	deletePostInTransaction = func(context.Context, uint, uint) (postDeleteResult, error) {
		return postDeleteResult{ParentPostID: &parentID}, nil
	}
	var invalidated []uint
	invalidatePostDeleteDetailCache = func(postID uint) error {
		invalidated = append(invalidated, postID)
		return nil
	}

	ctx, recorder := newPostDeleteContext("42", &viewerID)
	DeletePost(ctx)
	ctx.Writer.WriteHeaderNow()
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(invalidated) != 2 || invalidated[0] != 42 || invalidated[1] != parentID {
		t.Fatalf("invalidated=%v want target then parent", invalidated)
	}
}

func TestDeletePostDoesNotCleanUpRejectedRequest(t *testing.T) {
	viewerID := uint(7)
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{name: "not found", err: errPostDeleteNotFound},
		{name: "forbidden", err: errPostDeleteForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stubPostDeleteDependencies(t, testCase.err)
			var calls int

			invalidatePostDeleteDetailCache = func(uint) error { calls++; return nil }
			cleanupDeletedPostLikeState = func(uint) error { calls++; return nil }
			ctx, _ := newPostDeleteContext("42", &viewerID)
			DeletePost(ctx)
			if calls != 0 {
				t.Fatalf("cleanup calls=%d", calls)
			}
		})
	}
}

func openPostDeleteIntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostRepost{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDeletePostIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openPostDeleteIntegrationDatabase(t)
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	owner := models.User{Username: "delete-owner-" + uuid.NewString(), Password: "test"}
	other := models.User{Username: "delete-other-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("author_id IN ?", []uint{owner.ID, other.ID}).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{owner.ID, other.ID}).Delete(&models.User{})
	})

	forbiddenArticle := models.Post{AuthorID: owner.ID, Content: "forbidden body", Visibility: "public"}
	if err := db.Create(&forbiddenArticle).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newPostDeleteContext(strconvPostID(forbiddenArticle.ID), &other.ID)
	DeletePost(ctx)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("forbidden status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	article := models.Post{AuthorID: owner.ID, Content: "delete fixture body", Visibility: "public"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PostRepost{UserID: other.ID, PostID: article.ID}).Error; err != nil {
		t.Fatal(err)
	}
	deleteOne := func(postID, viewerID uint) int {
		ctx, recorder := newPostDeleteContext(strconvPostID(postID), &viewerID)
		DeletePost(ctx)
		ctx.Writer.WriteHeaderNow()
		return recorder.Code
	}
	if status := deleteOne(article.ID, owner.ID); status != http.StatusNoContent {
		t.Fatalf("article=%d status=%d", article.ID, status)
	}
	if status := deleteOne(article.ID, owner.ID); status != http.StatusNotFound {
		t.Fatalf("repeat delete status=%d", status)
	}
	var deleted models.Post
	if err := db.Unscoped().First(&deleted, article.ID).Error; err != nil || !deleted.DeletedAt.Valid {
		t.Fatalf("soft deleted article=%#v err=%v", deleted, err)
	}
	var remainingReposts int64
	if err := db.Model(&models.PostRepost{}).Where("post_id = ?", article.ID).Count(&remainingReposts).Error; err != nil {
		t.Fatal(err)
	}
	if remainingReposts != 0 {
		t.Fatalf("reposts remaining after article delete=%d", remainingReposts)
	}

	raceArticle := models.Post{AuthorID: owner.ID, Content: "race body", Visibility: "public"}
	if err := db.Create(&raceArticle).Error; err != nil {
		t.Fatal(err)
	}
	var waitGroup sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		waitGroup.Add(1)
		go func() { defer waitGroup.Done(); statuses <- deleteOne(raceArticle.ID, owner.ID) }()
	}
	waitGroup.Wait()
	close(statuses)
	successes, notFound := 0, 0
	for status := range statuses {
		switch status {
		case http.StatusNoContent:
			successes++
		case http.StatusNotFound:
			notFound++
		default:
			t.Fatalf("race status=%d", status)
		}
	}
	if successes != 1 || notFound != 1 {
		t.Fatalf("race successes=%d notFound=%d", successes, notFound)
	}
}

func TestDeletePostCancellationRollsBackReplyAndRepostChanges(t *testing.T) {
	db := openPostDeleteIntegrationDatabase(t)
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	owner := models.User{Username: "delete-cancel-owner-" + uuid.NewString(), Password: "test"}
	reposter := models.User{Username: "delete-cancel-reposter-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&[]*models.User{&owner, &reposter}).Error; err != nil {
		t.Fatal(err)
	}
	parent := models.Post{AuthorID: owner.ID, Content: "delete cancellation parent", Visibility: "public", ReplyCount: 1}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	child := models.Post{AuthorID: owner.ID, Content: "delete cancellation child", Visibility: "public", ReplyToPostID: &parent.ID, ConversationID: &parent.ID}
	if err := db.Create(&child).Error; err != nil {
		t.Fatal(err)
	}
	repost := models.PostRepost{UserID: reposter.ID, PostID: child.ID}
	if err := db.Create(&repost).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN ?", []uint{parent.ID, child.ID}).Delete(&models.PostRepost{})
		db.Unscoped().Where("id IN ?", []uint{parent.ID, child.ID}).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{owner.ID, reposter.ID}).Delete(&models.User{})
	})

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	sequenceName := "post_delete_cancel_signal_" + suffix
	functionName := "post_delete_cancel_delay_" + suffix
	triggerName := "post_delete_cancel_trigger_" + suffix
	if err := db.Exec("CREATE SEQUENCE " + sequenceName).Error; err != nil {
		t.Fatalf("create trigger signal sequence: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Exec("DROP TRIGGER IF EXISTS " + triggerName + " ON posts").Error
		_ = db.Exec("DROP FUNCTION IF EXISTS " + functionName + "()").Error
		_ = db.Exec("DROP SEQUENCE IF EXISTS " + sequenceName).Error
	})
	functionSQL := "CREATE FUNCTION " + functionName + `() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
    PERFORM nextval('` + sequenceName + `');
    PERFORM pg_sleep(8);
  END IF;
  RETURN NEW;
END;
$$`
	if err := db.Exec(functionSQL).Error; err != nil {
		t.Fatalf("create soft-delete delay trigger function: %v", err)
	}
	if err := db.Exec("CREATE TRIGGER " + triggerName + " BEFORE UPDATE ON posts FOR EACH ROW EXECUTE FUNCTION " + functionName + "()").Error; err != nil {
		t.Fatalf("create soft-delete delay trigger: %v", err)
	}

	requestContext, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := deletePostInTransactionFromDB(requestContext, child.ID, owner.ID)
		result <- err
	}()

	signalDeadline := time.Now().Add(5 * time.Second)
	triggerEntered := false
	for time.Now().Before(signalDeadline) {
		var isCalled bool
		if err := db.Raw("SELECT is_called FROM " + sequenceName).Scan(&isCalled).Error; err != nil {
			cancel()
			t.Fatalf("observe soft-delete trigger: %v", err)
		}
		if isCalled {
			triggerEntered = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !triggerEntered {
		cancel()
		t.Fatal("delete transaction did not reach the soft-delete trigger")
	}
	cancel()

	var deleteErr error
	select {
	case deleteErr = <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled delete transaction did not return promptly")
	}
	if deleteErr == nil || (!errors.Is(deleteErr, context.Canceled) && !isPostgresTimeoutError(deleteErr)) {
		t.Fatalf("delete error=%v, want context cancellation or PostgreSQL query cancellation", deleteErr)
	}

	var storedChild models.Post
	if err := db.Unscoped().First(&storedChild, child.ID).Error; err != nil {
		t.Fatalf("reload child after rollback: %v", err)
	}
	if storedChild.DeletedAt.Valid {
		t.Fatal("child post was deleted despite transaction cancellation")
	}
	var storedParent models.Post
	if err := db.First(&storedParent, parent.ID).Error; err != nil {
		t.Fatalf("reload parent after rollback: %v", err)
	}
	if storedParent.ReplyCount != 1 {
		t.Fatalf("parent reply_count=%d after rollback, want 1", storedParent.ReplyCount)
	}
	var repostCount int64
	if err := db.Model(&models.PostRepost{}).Where("post_id = ? AND user_id = ?", child.ID, reposter.ID).Count(&repostCount).Error; err != nil {
		t.Fatalf("count repost after rollback: %v", err)
	}
	if repostCount != 1 {
		t.Fatalf("repost rows=%d after rollback, want 1", repostCount)
	}
}

func strconvPostID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}
