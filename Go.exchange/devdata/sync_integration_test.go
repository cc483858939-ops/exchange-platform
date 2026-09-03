package devdata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/initialize"
	"Go.exchange/models"

	"github.com/pgvector/pgvector-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type syncIntegrationData struct {
	Registry  SourceRegistry
	SourceIDs map[string]string
	Tag       string
	Base      int64
}

func openDevDataIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run DevData PostgreSQL integration tests")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open PostgreSQL test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get PostgreSQL test database handle: %v", err)
	}
	previous := global.Db
	global.Db = db
	t.Cleanup(func() {
		global.Db = previous
		_ = sqlDB.Close()
	})
	if err := initialize.RunMigrations(); err != nil {
		t.Fatalf("run DevData integration migrations: %v", err)
	}
	return db
}

func newSyncIntegrationData() syncIntegrationData {
	base := int64(700000000000000000) + time.Now().UnixNano()%90000000000000000
	tag := strconv.FormatInt(base%1000000, 10)
	registry := SourceRegistry{
		Version:         SourceRegistryVersion,
		DefaultMaxPosts: DefaultMaxPosts,
		Accounts: []SourceAccount{
			{Key: "it_a_" + tag, Platform: "x", Handle: "it_a_" + tag, Category: "integration", MaxPosts: DefaultMaxPosts, Enabled: true},
			{Key: "it_b_" + tag, Platform: "x", Handle: "it_b_" + tag, Category: "integration", MaxPosts: DefaultMaxPosts, Enabled: true},
			{Key: "it_c_" + tag, Platform: "x", Handle: "it_c_" + tag, Category: "integration", MaxPosts: DefaultMaxPosts, Enabled: true},
		},
	}
	sourceIDs := make(map[string]string, len(registry.Accounts))
	for index, account := range registry.Accounts {
		sourceIDs[account.Key] = strconv.FormatInt(base+int64(index+1), 10)
	}
	return syncIntegrationData{Registry: registry, SourceIDs: sourceIDs, Tag: tag, Base: base}
}

func (data syncIntegrationData) account(key string) SourceAccount {
	account, ok := data.Registry.AccountByKey(key)
	if !ok {
		panic("missing integration account " + key)
	}
	return account
}

func (data syncIntegrationData) sourcePost(key string, offset int64, at time.Time, label string) SnapshotPost {
	account := data.account(key)
	postID := strconv.FormatInt(data.Base+offset, 10)
	return SnapshotPost{
		RegistryKey:  key,
		SourcePostID: postID,
		SourceURL:    fmt.Sprintf("https://x.com/%s/status/%s", account.Handle, postID),
		Text:         fmt.Sprintf("it-marker-%s %s source content", data.Tag, label),
		CreatedAt:    at.UTC(),
		Language:     "en",
		SourceMetrics: SourceMetrics{
			LikeCount:   offset,
			ReplyCount:  offset + 1,
			RepostCount: offset + 2,
			QuoteCount:  offset + 3,
		},
	}
}

func (data syncIntegrationData) snapshot(fetchedAt time.Time, posts ...SnapshotPost) Snapshot {
	accounts := make([]SnapshotAccount, 0, len(data.Registry.Accounts))
	for _, configured := range data.Registry.Accounts {
		accounts = append(accounts, SnapshotAccount{
			RegistryKey:     configured.Key,
			SourceUserID:    data.SourceIDs[configured.Key],
			Handle:          configured.Handle,
			Name:            "Integration " + configured.Key,
			Description:     "Integration profile " + data.Tag,
			ProfileImageURL: "https://img.example.test/" + configured.Key,
			Category:        configured.Category,
		})
	}
	return Snapshot{
		Version:   DefaultSnapshotVersion,
		FetchedAt: fetchedAt.UTC(),
		Accounts:  accounts,
		Posts:     append([]SnapshotPost(nil), posts...),
	}
}

func integrationPostIDs(db *gorm.DB, accountIDs []uint) []uint {
	var mappings []models.DevDataMirrorPost
	if len(accountIDs) > 0 {
		db.Unscoped().Where("mirror_account_id IN ?", accountIDs).Find(&mappings)
	}
	ids := make([]uint, 0, len(mappings))
	for _, mapping := range mappings {
		if mapping.LocalPostID != 0 {
			ids = append(ids, mapping.LocalPostID)
		}
	}
	return ids
}

func cleanupDevDataIntegrationRows(db *gorm.DB, data syncIntegrationData) {
	var accounts []models.DevDataMirrorAccount
	db.Unscoped().Where("registry_key LIKE ?", "it_%_"+data.Tag).Find(&accounts)
	accountIDs := make([]uint, 0, len(accounts))
	for _, account := range accounts {
		accountIDs = append(accountIDs, account.ID)
	}
	postIDs := integrationPostIDs(db, accountIDs)
	var users []models.User
	db.Unscoped().Where("username LIKE ? OR username LIKE ?", "x_it_%_"+data.Tag, "it_%_"+data.Tag).Find(&users)
	var markerPosts []models.Post
	db.Unscoped().Where("content LIKE ?", "it-marker-"+data.Tag+"%").Find(&markerPosts)
	userIDs := make([]uint, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, user.ID)
	}
	for _, post := range markerPosts {
		postIDs = append(postIDs, post.ID)
	}
	postIDs = uniqueUintIDs(postIDs)
	if len(postIDs) > 0 {
		// Notifications use RESTRICT while the remaining derived rows rely on
		// the canonical post cascade graph. Delete all test-only rows first.
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.Notification{})
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostReaction{})
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostBehavior{})
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.UserPostRecoState{})
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostRepost{})
		db.Unscoped().Model(&models.Post{}).Where("id IN ?", postIDs).Updates(map[string]interface{}{
			"reply_to_post_id": nil,
			"quote_post_id":    nil,
			"conversation_id":  nil,
		})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
	}
	if len(accountIDs) > 0 {
		db.Unscoped().Where("mirror_account_id IN ?", accountIDs).Delete(&models.DevDataMirrorPost{})
		db.Unscoped().Where("id IN ?", accountIDs).Delete(&models.DevDataMirrorAccount{})
	}
	if len(userIDs) > 0 {
		db.Unscoped().Where("follower_id IN ? OR following_id IN ?", userIDs, userIDs).Delete(&models.UserFollow{})
		db.Unscoped().Where("recipient_id IN ? OR actor_id IN ?", userIDs, userIDs).Delete(&models.Notification{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.PostReaction{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.PostBehavior{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.UserPostRecoState{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.PostRepost{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	}
}

func uniqueUintIDs(values []uint) []uint {
	seen := make(map[uint]struct{}, len(values))
	result := make([]uint, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func findMirrorAccount(t *testing.T, db *gorm.DB, key string) models.DevDataMirrorAccount {
	t.Helper()
	var account models.DevDataMirrorAccount
	if err := db.Where("registry_key = ?", key).First(&account).Error; err != nil {
		t.Fatalf("load mirror account %q: %v", key, err)
	}
	return account
}

func findMirrorMapping(t *testing.T, db *gorm.DB, sourcePostID string) models.DevDataMirrorPost {
	t.Helper()
	var mapping models.DevDataMirrorPost
	if err := db.Where("source_post_id = ?", sourcePostID).First(&mapping).Error; err != nil {
		t.Fatalf("load mirror mapping %q: %v", sourcePostID, err)
	}
	return mapping
}

func TestDevDataMirrorSyncLifecycleIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	aKey := data.Registry.Accounts[0].Key
	bKey := data.Registry.Accounts[1].Key
	cKey := data.Registry.Accounts[2].Key
	replyRoot := data.sourcePost(aKey, 1001, now.Add(-time.Hour), "reply-root")
	keepRoot := data.sourcePost(aKey, 1002, now.Add(-2*time.Hour), "keep-root")
	quoteRoot := data.sourcePost(bKey, 1003, now.Add(-3*time.Hour), "quote-root")
	conversationRoot := data.sourcePost(cKey, 1004, now.Add(-4*time.Hour), "conversation-root")
	hardRoot := data.sourcePost(cKey, 1005, now.Add(-5*time.Hour), "hard-root")
	initial := data.snapshot(now, replyRoot, keepRoot, quoteRoot, conversationRoot, hardRoot)

	first, err := SyncSnapshot(context.Background(), db, data.Registry, initial, nil, now)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if first.Inserted != 5 || first.MirrorUsers != 3 || first.ActiveImportedRoots != 5 || first.Tombstones != 0 {
		t.Fatalf("first sync result=%#v", first)
	}

	accounts := make(map[string]models.DevDataMirrorAccount, len(data.Registry.Accounts))
	for _, configured := range data.Registry.Accounts {
		accounts[configured.Key] = findMirrorAccount(t, db, configured.Key)
		if accounts[configured.Key].LocalUserID == 0 || accounts[configured.Key].SourceUserID != data.SourceIDs[configured.Key] {
			t.Fatalf("bad mirror account=%#v", accounts[configured.Key])
		}
	}
	localPostIDs := make(map[string]uint)
	for _, sourcePost := range initial.Posts {
		mapping := findMirrorMapping(t, db, sourcePost.SourcePostID)
		localPostIDs[sourcePost.SourcePostID] = mapping.LocalPostID
		var post models.Post
		if err := db.First(&post, mapping.LocalPostID).Error; err != nil {
			t.Fatalf("load imported Post %q: %v", sourcePost.SourcePostID, err)
		}
		if post.AuthorID != accounts[sourcePost.RegistryKey].LocalUserID || post.Content != sourcePost.Text || post.Visibility != "public" || !post.CreatedAt.Equal(sourcePost.CreatedAt) || post.ReplyToPostID != nil || post.QuotePostID != nil || post.ConversationID != nil {
			t.Fatalf("imported Post=%#v source=%#v", post, sourcePost)
		}
		if post.LikeCount != 0 || post.ReplyCount != 0 || post.ViewCount != 0 || post.LikeSyncVersion != 0 {
			t.Fatalf("imported counters=%#v", post)
		}
	}
	var notificationCount, behaviorCount, reactionCount, outboxCount int64
	db.Model(&models.Notification{}).Where("post_id IN ?", localPostIDs).Count(&notificationCount)
	db.Model(&models.PostBehavior{}).Where("post_id IN ?", localPostIDs).Count(&behaviorCount)
	db.Model(&models.PostReaction{}).Where("post_id IN ?", localPostIDs).Count(&reactionCount)
	stringPostIDs := make([]string, 0, len(localPostIDs))
	for _, postID := range localPostIDs {
		stringPostIDs = append(stringPostIDs, strconv.FormatUint(uint64(postID), 10))
	}
	db.Model(&models.OutboxEvent{}).Where("aggregate_id IN ?", stringPostIDs).Count(&outboxCount)
	if notificationCount != 0 || behaviorCount != 0 || reactionCount != 0 || outboxCount != 0 {
		t.Fatalf("direct import created side effects notifications=%d behaviors=%d reactions=%d outbox=%d", notificationCount, behaviorCount, reactionCount, outboxCount)
	}

	viewer := models.User{Username: "it_viewer_" + data.Tag, DisplayName: "Integration Viewer"}
	if err := db.Create(&viewer).Error; err != nil {
		t.Fatalf("create integration viewer: %v", err)
	}
	follow := models.UserFollow{FollowerID: viewer.ID, FollowingID: accounts[aKey].LocalUserID}
	if err := db.Create(&follow).Error; err != nil {
		t.Fatalf("create follow: %v", err)
	}

	keepLocalID := localPostIDs[keepRoot.SourcePostID]
	if err := db.Model(&models.Post{}).Where("id = ?", keepLocalID).Updates(map[string]interface{}{
		"like_count":        7,
		"reply_count":       3,
		"view_count":        9,
		"like_sync_version": 4,
	}).Error; err != nil {
		t.Fatalf("seed local engagement counters: %v", err)
	}
	var replyTo, quoteTo, conversationTo = localPostIDs[replyRoot.SourcePostID], localPostIDs[quoteRoot.SourcePostID], localPostIDs[conversationRoot.SourcePostID]
	reply := models.Post{Model: gorm.Model{CreatedAt: now.Add(-30 * time.Minute), UpdatedAt: now.Add(-30 * time.Minute)}, AuthorID: viewer.ID, Content: "it-marker-" + data.Tag + " real reply", Visibility: "public", ReplyToPostID: &replyTo, ConversationID: &replyTo}
	quote := models.Post{Model: gorm.Model{CreatedAt: now.Add(-25 * time.Minute), UpdatedAt: now.Add(-25 * time.Minute)}, AuthorID: viewer.ID, Content: "it-marker-" + data.Tag + " real quote", Visibility: "public", QuotePostID: &quoteTo}
	conversationReply := models.Post{Model: gorm.Model{CreatedAt: now.Add(-20 * time.Minute), UpdatedAt: now.Add(-20 * time.Minute)}, AuthorID: viewer.ID, Content: "it-marker-" + data.Tag + " conversation child", Visibility: "public", ReplyToPostID: &keepLocalID, ConversationID: &conversationTo}
	for name, post := range map[string]*models.Post{"reply": &reply, "quote": &quote, "conversation": &conversationReply} {
		if err := db.Create(post).Error; err != nil {
			t.Fatalf("create %s dependency: %v", name, err)
		}
	}

	hardLocalID := localPostIDs[hardRoot.SourcePostID]
	notificationPostID := hardLocalID
	derived := []interface{}{
		&models.PostRepost{UserID: viewer.ID, PostID: hardLocalID, CreatedAt: now.Add(-10 * time.Minute)},
		&models.PostReaction{UserID: viewer.ID, PostID: hardLocalID, Reaction: models.PostReactionLike, Liked: true, Version: 1, UpdatedAt: now, StateChangedAt: now},
		&models.PostBehavior{Model: gorm.Model{CreatedAt: now, UpdatedAt: now}, UserID: viewer.ID, PostID: hardLocalID, Action: "view", Count: 1, LastSeenAt: now, Active: true, BehaviorVersion: 1},
		&models.PostEmbedding{PostID: hardLocalID, Version: "integration", Model: "integration", Dimensions: 2, Embedding: pgvector.NewVector([]float32{0.1, 0.2}), ContentHash: SourceTextContentHash(hardRoot.Text), CreatedAt: now, UpdatedAt: now},
		&models.UserPostRecoState{UserID: viewer.ID, PostID: hardLocalID, Interacted: true, CanonicalVersion: "integration", RebuiltAt: now},
		&models.Notification{RecipientID: viewer.ID, ActorID: accounts[aKey].LocalUserID, Type: models.NotificationTypePostLiked, PostID: &notificationPostID, DedupeKey: "it-" + data.Tag + "-notification", SourceVersion: 1, ActivityAt: now, CreatedAt: now, UpdatedAt: now},
	}
	for _, row := range derived {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("create derived hard-delete row %T: %v", row, err)
		}
	}

	same, err := SyncSnapshot(context.Background(), db, data.Registry, initial, nil, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("idempotent sync: %v", err)
	}
	if same.Inserted != 0 || same.Kept != 5 || same.Reactivated != 0 || same.RetiredSoft != 0 || same.RetiredHard != 0 {
		t.Fatalf("idempotent result=%#v", same)
	}
	var kept models.Post
	if err := db.First(&kept, keepLocalID).Error; err != nil {
		t.Fatalf("load kept Post: %v", err)
	}
	if kept.LikeCount != 7 || kept.ReplyCount != 3 || kept.ViewCount != 9 || kept.LikeSyncVersion != 4 {
		t.Fatalf("local counters were overwritten=%#v", kept)
	}
	var followCount int64
	db.Model(&models.UserFollow{}).Where("id = ?", follow.ID).Count(&followCount)
	if followCount != 1 {
		t.Fatalf("follow was not preserved after idempotent sync")
	}

	changed := initial
	changed.Accounts = append([]SnapshotAccount(nil), initial.Accounts...)
	changed.Accounts[0].Name = "Changed integration profile"
	changed.Accounts[0].Description = "Changed integration description"
	changed.Posts = append([]SnapshotPost(nil), initial.Posts...)
	for index := range changed.Posts {
		if changed.Posts[index].SourcePostID == keepRoot.SourcePostID {
			changed.Posts[index].Text = "it-marker-" + data.Tag + " changed source content"
			changed.Posts[index].SourceMetrics.LikeCount = 99
		}
	}
	changedResult, err := SyncSnapshot(context.Background(), db, data.Registry, changed, nil, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("changed KEEP sync: %v", err)
	}
	if changedResult.Kept != 5 || !containsUint(changedResult.AffectedPostIDs, keepLocalID) {
		t.Fatalf("changed KEEP result=%#v", changedResult)
	}
	if findMirrorMapping(t, db, keepRoot.SourcePostID).LocalPostID != keepLocalID {
		t.Fatalf("KEEP recreated local Post")
	}
	if err := db.First(&kept, keepLocalID).Error; err != nil {
		t.Fatalf("reload changed kept Post: %v", err)
	}
	if kept.Content != "it-marker-"+data.Tag+" changed source content" || kept.LikeCount != 7 || kept.ReplyCount != 3 || kept.ViewCount != 9 || kept.LikeSyncVersion != 4 {
		t.Fatalf("KEEP content/counters=%#v", kept)
	}
	var changedMirrorUser models.User
	if err := db.First(&changedMirrorUser, accounts[aKey].LocalUserID).Error; err != nil {
		t.Fatalf("reload changed mirror user: %v", err)
	}
	if changedMirrorUser.DisplayName != "Changed integration profile" || changedMirrorUser.Bio != "Changed integration description" {
		t.Fatalf("mirror profile=%#v", changedMirrorUser)
	}

	mismatch := changed
	mismatch.Accounts = append([]SnapshotAccount(nil), changed.Accounts...)
	mismatch.Accounts[0].SourceUserID = strconv.FormatInt(data.Base+9999, 10)
	if _, err := SyncSnapshot(context.Background(), db, data.Registry, mismatch, nil, now.Add(3*time.Minute)); !errors.Is(err, ErrSourceIdentityMismatch) {
		t.Fatalf("source identity mismatch error=%v", err)
	}
	if findMirrorAccount(t, db, aKey).SourceUserID != data.SourceIDs[aKey] {
		t.Fatalf("source identity changed after rejected mismatch")
	}

	desired := data.snapshot(now.Add(4*time.Minute), keepRoot)
	retired, err := SyncSnapshot(context.Background(), db, data.Registry, desired, nil, now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("retire sync: %v", err)
	}
	if retired.RetiredSoft != 3 || retired.RetiredHard != 1 || retired.Tombstones != 3 || retired.ActiveImportedRoots != 1 {
		t.Fatalf("retire result=%#v", retired)
	}
	for _, sourcePostID := range []string{replyRoot.SourcePostID, quoteRoot.SourcePostID, conversationRoot.SourcePostID} {
		mapping := findMirrorMapping(t, db, sourcePostID)
		if mapping.State != models.DevDataMirrorPostStateTombstone {
			t.Fatalf("mapping %s state=%q", sourcePostID, mapping.State)
		}
		var post models.Post
		if err := db.Unscoped().First(&post, mapping.LocalPostID).Error; err != nil || !post.DeletedAt.Valid {
			t.Fatalf("tombstone Post %s post=%#v err=%v", sourcePostID, post, err)
		}
	}
	var hardDeleted models.Post
	if err := db.Unscoped().First(&hardDeleted, hardLocalID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("hard-deleted Post still exists: post=%#v err=%v", hardDeleted, err)
	}
	var hardMapping models.DevDataMirrorPost
	if err := db.Unscoped().Where("source_post_id = ?", hardRoot.SourcePostID).First(&hardMapping).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("hard-deleted mapping still exists: mapping=%#v err=%v", hardMapping, err)
	}
	for name, query := range map[string]*gorm.DB{
		"repost":       db.Unscoped().Where("post_id = ?", hardLocalID).Find(&[]models.PostRepost{}),
		"reaction":     db.Unscoped().Where("post_id = ?", hardLocalID).Find(&[]models.PostReaction{}),
		"behavior":     db.Unscoped().Where("post_id = ?", hardLocalID).Find(&[]models.PostBehavior{}),
		"embedding":    db.Unscoped().Where("post_id = ?", hardLocalID).Find(&[]models.PostEmbedding{}),
		"reco_state":   db.Unscoped().Where("post_id = ?", hardLocalID).Find(&[]models.UserPostRecoState{}),
		"notification": db.Unscoped().Where("post_id = ?", hardLocalID).Find(&[]models.Notification{}),
	} {
		if query.Error != nil || query.RowsAffected != 0 {
			t.Fatalf("hard-delete derived %s error=%v rows=%d", name, query.Error, query.RowsAffected)
		}
	}
	var preservedReply, preservedQuote, preservedConversation models.Post
	for name, postID := range map[string]uint{"reply": reply.ID, "quote": quote.ID, "conversation": conversationReply.ID} {
		var target *models.Post
		switch name {
		case "reply":
			target = &preservedReply
		case "quote":
			target = &preservedQuote
		default:
			target = &preservedConversation
		}
		if err := db.Unscoped().First(target, postID).Error; err != nil || target.DeletedAt.Valid {
			t.Fatalf("preserved %s=%#v err=%v", name, target, err)
		}
	}
	db.Model(&models.UserFollow{}).Where("id = ?", follow.ID).Count(&followCount)
	if followCount != 1 {
		t.Fatalf("follow was not preserved after retirement")
	}
	verification, err := VerifyCore(context.Background(), db, data.Registry, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("cold-start verification: %v", err)
	}
	if verification.RecentCandidates == 0 || verification.FinalColdFeed == 0 || verification.ActiveImportedRoots != 1 {
		t.Fatalf("verification=%#v", verification)
	}

	reactivatedSnapshot := data.snapshot(now.Add(6*time.Minute), keepRoot, replyRoot)
	reactivated, err := SyncSnapshot(context.Background(), db, data.Registry, reactivatedSnapshot, nil, now.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("reactivate sync: %v", err)
	}
	if reactivated.Reactivated != 1 || !containsUint(reactivated.AffectedPostIDs, localPostIDs[replyRoot.SourcePostID]) {
		t.Fatalf("reactivate result=%#v", reactivated)
	}
	reactivatedMapping := findMirrorMapping(t, db, replyRoot.SourcePostID)
	if reactivatedMapping.LocalPostID != localPostIDs[replyRoot.SourcePostID] || reactivatedMapping.State != models.DevDataMirrorPostStateActive {
		t.Fatalf("reactivated mapping=%#v", reactivatedMapping)
	}
	var reactivatedPost models.Post
	if err := db.Unscoped().First(&reactivatedPost, reactivatedMapping.LocalPostID).Error; err != nil || reactivatedPost.DeletedAt.Valid {
		t.Fatalf("reactivated Post=%#v err=%v", reactivatedPost, err)
	}
	if err := db.Unscoped().First(&preservedReply, reply.ID).Error; err != nil {
		t.Fatalf("reply disappeared on reactivation: %v", err)
	}

	if _, err := SyncSnapshot(context.Background(), db, data.Registry, desired, nil, now.Add(7*time.Minute)); err != nil {
		t.Fatalf("retire reactivated root: %v", err)
	}
	if err := db.Unscoped().Where("id = ?", reply.ID).Delete(&models.Post{}).Error; err != nil {
		t.Fatalf("delete reply for tombstone GC: %v", err)
	}
	gcReply, err := SyncSnapshot(context.Background(), db, data.Registry, desired, nil, now.Add(8*time.Minute))
	if err != nil {
		t.Fatalf("reply tombstone GC: %v", err)
	}
	if gcReply.RetiredHard != 1 {
		t.Fatalf("reply tombstone GC result=%#v", gcReply)
	}
	if err := db.Unscoped().Where("id IN ?", []uint{quote.ID, conversationReply.ID}).Delete(&models.Post{}).Error; err != nil {
		t.Fatalf("delete quote/conversation for tombstone GC: %v", err)
	}
	gcAll, err := SyncSnapshot(context.Background(), db, data.Registry, desired, nil, now.Add(9*time.Minute))
	if err != nil {
		t.Fatalf("remaining tombstone GC: %v", err)
	}
	if gcAll.Tombstones != 0 || gcAll.ActiveImportedRoots != 1 || gcAll.RetiredHard != 2 {
		t.Fatalf("remaining GC result=%#v", gcAll)
	}

	collisionKey := "it_d_" + data.Tag
	collisionUser := models.User{Username: MirrorUsername(collisionKey), DisplayName: "collision"}
	if err := db.Create(&collisionUser).Error; err != nil {
		t.Fatalf("create collision user: %v", err)
	}
	extended := data.Registry
	extended.Accounts = append(append([]SourceAccount(nil), data.Registry.Accounts...), SourceAccount{Key: collisionKey, Platform: "x", Handle: collisionKey, Category: "integration", MaxPosts: DefaultMaxPosts, Enabled: true})
	extendedSnapshot := desired
	extendedSnapshot.Accounts = append([]SnapshotAccount(nil), desired.Accounts...)
	extendedSnapshot.Accounts = append(extendedSnapshot.Accounts, SnapshotAccount{RegistryKey: collisionKey, SourceUserID: strconv.FormatInt(data.Base+9998, 10), Handle: collisionKey, Name: "Collision source", Category: "integration"})
	if _, err := SyncSnapshot(context.Background(), db, extended, extendedSnapshot, nil, now.Add(10*time.Minute)); !errors.Is(err, ErrMirrorUsernameCollision) {
		t.Fatalf("mirror username collision error=%v", err)
	}
}

func TestDevDataVerifyFailsWhenRecommendationPathIsEmptyIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	if _, err := SyncSnapshot(context.Background(), db, data.Registry, data.snapshot(now), nil, now); err != nil {
		t.Fatalf("seed empty DevData inventory: %v", err)
	}
	if _, err := VerifyCore(context.Background(), db, data.Registry, now); err == nil || !strings.Contains(err.Error(), "actual recommendation path returned no recent recall candidates") {
		t.Fatalf("empty recommendation path error=%v", err)
	}
}

func TestDevDataVerifyRejectsUnrelatedRecommendationInventoryIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	if _, err := SyncSnapshot(context.Background(), db, data.Registry, data.snapshot(now), nil, now); err != nil {
		t.Fatalf("seed empty DevData inventory: %v", err)
	}
	author := models.User{Username: "it_unrelated_author_" + data.Tag}
	if err := db.Create(&author).Error; err != nil {
		t.Fatalf("create unrelated author: %v", err)
	}
	post := models.Post{Model: gorm.Model{CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)}, AuthorID: author.ID, Content: "it-marker-" + data.Tag + " unrelated recommendation", Visibility: "public"}
	if err := db.Create(&post).Error; err != nil {
		t.Fatalf("create unrelated post: %v", err)
	}
	if _, err := VerifyCore(context.Background(), db, data.Registry, now); err == nil || !strings.Contains(err.Error(), "no active DevData imported Post") {
		t.Fatalf("unrelated recommendation inventory error=%v", err)
	}
}

func containsUint(values []uint, wanted uint) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestDevDataRetireReplyRaceIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	root := data.sourcePost(data.Registry.Accounts[0].Key, 1101, now.Add(-time.Hour), "race-root")
	initial := data.snapshot(now, root)
	if _, err := SyncSnapshot(context.Background(), db, data.Registry, initial, nil, now); err != nil {
		t.Fatalf("seed race root: %v", err)
	}
	mapping := findMirrorMapping(t, db, root.SourcePostID)
	viewer := models.User{Username: "it_race_viewer_" + data.Tag}
	if err := db.Create(&viewer).Error; err != nil {
		t.Fatalf("create race viewer: %v", err)
	}
	replyTo := mapping.LocalPostID
	reply := models.Post{Model: gorm.Model{CreatedAt: now, UpdatedAt: now}, AuthorID: viewer.ID, Content: "it-marker-" + data.Tag + " race reply", Visibility: "public", ReplyToPostID: &replyTo, ConversationID: &replyTo}
	desired := data.snapshot(now.Add(time.Minute))

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var syncErr, replyErr error
	go func() {
		defer wait.Done()
		<-start
		_, syncErr = SyncSnapshot(context.Background(), db, data.Registry, desired, nil, now.Add(2*time.Minute))
	}()
	replyTx := db.Begin()
	if replyTx.Error != nil {
		t.Fatalf("begin concurrent reply transaction: %v", replyTx.Error)
	}
	go func() {
		defer wait.Done()
		<-start
		if err := replyTx.Create(&reply).Error; err != nil {
			replyTx.Rollback()
			replyErr = err
			return
		}
		replyErr = replyTx.Commit().Error
	}()
	close(start)
	wait.Wait()
	if syncErr != nil {
		t.Fatalf("concurrent retire: %v", syncErr)
	}

	var finalMapping models.DevDataMirrorPost
	mappingErr := db.Where("source_post_id = ?", root.SourcePostID).First(&finalMapping).Error
	var finalRoot models.Post
	rootErr := db.Unscoped().First(&finalRoot, mapping.LocalPostID).Error
	switch {
	case rootErr == nil:
		if mappingErr != nil || finalMapping.State != models.DevDataMirrorPostStateTombstone || !finalRoot.DeletedAt.Valid {
			t.Fatalf("reply-wins final mapping=%#v mappingErr=%v root=%#v", finalMapping, mappingErr, finalRoot)
		}
		if replyErr != nil {
			t.Fatalf("reply should survive when lock winner is reply: %v", replyErr)
		}
		var persistedReply models.Post
		if err := db.Unscoped().Where("id = ?", reply.ID).First(&persistedReply).Error; err != nil {
			t.Fatalf("reply disappeared after tombstone: %v", err)
		}
	case errors.Is(rootErr, gorm.ErrRecordNotFound):
		if !errors.Is(mappingErr, gorm.ErrRecordNotFound) || replyErr == nil {
			t.Fatalf("retire-wins final mappingErr=%v rootErr=%v replyErr=%v", mappingErr, rootErr, replyErr)
		}
	default:
		t.Fatalf("load final race root: %v", rootErr)
	}
}
