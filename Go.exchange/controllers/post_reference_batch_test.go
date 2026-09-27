package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLoadPostReferencesByIDsEmptyWithoutDatabase(t *testing.T) {
	references, err := loadPostReferencesByIDsFromDB(nil, []uint{0, 0}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if references == nil || len(references) != 0 {
		t.Fatalf("references=%#v want a non-nil empty map", references)
	}

	responses := []postResponse{{ReplyToPost: &postReferenceResponse{ID: 1}, QuotePost: &postReferenceResponse{ID: 2}}}
	if err := hydratePostResponsesReferencesFromDB(nil, responses, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if responses[0].ReplyToPost != nil || responses[0].QuotePost != nil {
		t.Fatalf("references without IDs were not cleared: %#v", responses[0])
	}
}

func TestLoadPostReferencesByIDsPropagatesDatabaseInitializationError(t *testing.T) {
	if _, err := loadPostReferencesByIDsFromDB(nil, []uint{42}, time.Now().UTC()); err == nil {
		t.Fatal("expected database initialization error")
	}
}

func TestLoadPostReferencesBatchesMixedReferencesAndPreservesTombstonesIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)

	activeWithMedia := createReferenceBatchTestPost(t, db, fixture.Author.ID, "active with media", "public", now.Add(-time.Minute), nil, nil)
	activeWithoutMedia := createReferenceBatchTestPost(t, db, fixture.Author.ID, "active without media", "public", now.Add(-2*time.Minute), nil, nil)
	privatePost := createReferenceBatchTestPost(t, db, fixture.Author.ID, "private", "private", now.Add(-3*time.Minute), nil, nil)
	deletedPost := createReferenceBatchTestPost(t, db, fixture.Author.ID, "deleted", "public", now.Add(-4*time.Minute), nil, nil)
	deletedAuthor := models.User{Username: "reference-deleted-author-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}
	deletedAuthorPost := createReferenceBatchTestPost(t, db, deletedAuthor.ID, "deleted author", "public", now.Add(-5*time.Minute), nil, nil)
	createdPostIDs := []uint{activeWithMedia.ID, activeWithoutMedia.ID, privatePost.ID, deletedPost.ID, deletedAuthorPost.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN ?", createdPostIDs).Delete(&models.PostMedia{})
		db.Unscoped().Where("id IN ?", createdPostIDs).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", deletedAuthor.ID).Delete(&models.User{})
	})
	if err := db.Create(&[]models.PostMedia{
		{PostID: activeWithMedia.ID, MediaType: "image", URL: "second.jpg", LargeURL: "second-large.jpg", Width: 80, Height: 80, Position: 2, CreatedAt: now},
		{PostID: activeWithMedia.ID, MediaType: "image", URL: "first.jpg", LargeURL: "first-large.jpg", Width: 80, Height: 80, Position: 0, CreatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedPost).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}
	missingID := deletedAuthorPost.ID + 1000000
	zeroID := uint(0)

	responses := []postResponse{
		{ReplyToPostID: &activeWithMedia.ID, QuotePostID: &deletedPost.ID},
		{ReplyToPostID: &activeWithMedia.ID, QuotePostID: &privatePost.ID},
		{ReplyToPostID: &missingID, QuotePostID: &activeWithoutMedia.ID},
		{ReplyToPostID: &activeWithoutMedia.ID, QuotePostID: &zeroID},
		{ReplyToPostID: &deletedAuthorPost.ID},
	}
	queryLogger := &postDetailSQLLogger{Interface: logger.Default}
	if err := hydratePostResponsesReferencesFromDB(db.Session(&gorm.Session{Logger: queryLogger}), responses, now); err != nil {
		t.Fatal(err)
	}

	if got := responses[0].ReplyToPost; got == nil || got.Deleted || got.ID != activeWithMedia.ID || got.Author == nil || got.Content != activeWithMedia.Content {
		t.Fatalf("active reply reference=%#v", got)
	}
	if got := responses[0].QuotePost; got == nil {
		t.Fatal("soft-deleted quote should return a tombstone")
	} else {
		assertPostReferenceTombstone(t, got)
	}
	assertPostReferenceTombstone(t, responses[1].QuotePost)
	assertPostReferenceTombstone(t, responses[2].ReplyToPost)
	assertPostReferenceTombstone(t, responses[4].ReplyToPost)
	if got := responses[2].QuotePost; got == nil || got.Deleted || got.Media == nil || len(got.Media) != 0 {
		t.Fatalf("active no-media quote=%#v want media=[]", got)
	}
	if responses[3].QuotePost != nil {
		t.Fatalf("zero quote ID hydrated unexpectedly: %#v", responses[3].QuotePost)
	}
	if responses[0].ReplyToPost == responses[1].ReplyToPost {
		t.Fatal("duplicate references should be assigned as distinct response values")
	}
	responses[0].ReplyToPost.Content = "mutated copy"
	if responses[1].ReplyToPost.Content != activeWithMedia.Content {
		t.Fatal("mutating one duplicate reference changed another response")
	}
	if len(responses[1].ReplyToPost.Media) != 2 || responses[1].ReplyToPost.Media[0].URL != "first.jpg" || responses[1].ReplyToPost.Media[1].URL != "second.jpg" {
		t.Fatalf("media order=%#v want position order", responses[1].ReplyToPost.Media)
	}
	assertActivePostReferenceWire(t, responses[2].QuotePost)
	assertOneReferenceBatch(t, queryLogger.snapshot(), 1, 1, 1, 6)
}

func TestGormRecommendationResponseMapperBatchesPostReferencesIntegration(t *testing.T) {
	db := openProfileTimelineIntegrationDB(t)
	author := responseMapperTestAuthor(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	targets := make([]models.Post, 20)
	for index := range targets {
		targets[index] = createReferenceBatchTestPost(t, db, author.ID, "reference-target-"+strconvInt(index), "public", now.Add(-time.Minute), nil, nil)
	}
	roots := make([]models.Post, 20)
	for index := range roots {
		quoteID := targets[index].ID
		roots[index] = createReferenceBatchTestPost(t, db, author.ID, "reference-root-"+strconvInt(index), "public", now.Add(-time.Second), nil, &quoteID)
		roots[index].Author = author
	}
	createdPostIDs := make([]uint, 0, len(targets)+len(roots))
	rootIDs := make([]uint, 0, len(roots))
	for _, post := range roots {
		rootIDs = append(rootIDs, post.ID)
		createdPostIDs = append(createdPostIDs, post.ID)
	}
	for _, post := range targets {
		createdPostIDs = append(createdPostIDs, post.ID)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN ?", createdPostIDs).Delete(&models.PostMedia{})
		db.Unscoped().Where("id IN ?", rootIDs).Delete(&models.Post{})
		targetIDs := make([]uint, 0, len(targets))
		for _, post := range targets {
			targetIDs = append(targetIDs, post.ID)
		}
		db.Unscoped().Where("id IN ?", targetIDs).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", author.ID).Delete(&models.User{})
	})

	selected := make([]recommendation.SelectedCandidate, len(roots))
	for index, root := range roots {
		selected[index] = recommendation.SelectedCandidate{Post: root, Breakdown: recommendation.ScoreBreakdown{FinalScore: float64(index) / 10}}
	}
	mapper, err := NewGormRecommendationResponseMapper(db)
	if err != nil {
		t.Fatal(err)
	}

	queryLogger := &postDetailSQLLogger{Interface: logger.Default}
	mapper.db = db.Session(&gorm.Session{Logger: queryLogger})
	responses, err := mapper.Map(context.Background(), selected, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != len(selected) {
		t.Fatalf("response count=%d want %d", len(responses), len(selected))
	}
	for index, response := range responses {
		if response.Post.ID != roots[index].ID || response.Score != selected[index].Breakdown.FinalScore || response.Post.QuotePost == nil || response.Post.QuotePost.ID != targets[index].ID {
			t.Fatalf("response[%d]=%#v lost selected order, score, or reference", index, response)
		}
	}
	assertOneReferenceBatch(t, queryLogger.snapshot(), 1, 1, 2, 20)

	duplicateID := targets[0].ID
	duplicateSelected := append([]recommendation.SelectedCandidate(nil), selected...)
	for index := range duplicateSelected {
		id := duplicateID
		duplicateSelected[index].Post.QuotePostID = &id
	}
	duplicateLogger := &postDetailSQLLogger{Interface: logger.Default}
	mapper.db = db.Session(&gorm.Session{Logger: duplicateLogger})
	duplicateResponses, err := mapper.Map(context.Background(), duplicateSelected, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(duplicateResponses) != len(duplicateSelected) {
		t.Fatalf("duplicate response count=%d want %d", len(duplicateResponses), len(duplicateSelected))
	}
	for index, response := range duplicateResponses {
		if response.Post.QuotePost == nil || response.Post.QuotePost.ID != duplicateID {
			t.Fatalf("duplicate response[%d] reference=%#v want id=%d", index, response.Post.QuotePost, duplicateID)
		}
	}
	if duplicateResponses[0].Post.QuotePost == duplicateResponses[1].Post.QuotePost {
		t.Fatal("duplicate recommendation references should not share mutable response pointers")
	}
	assertOneReferenceBatch(t, duplicateLogger.snapshot(), 1, 1, 2, 1)
}

func TestLoadPostResponsesReferenceQueriesStayBoundedAsPageGrowsIntegration(t *testing.T) {
	db := openProfileTimelineIntegrationDB(t)
	author := responseMapperTestAuthor(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	runID := uuid.NewString()
	roots := make([]models.Post, 20)
	targetIDs := make([]uint, 0, len(roots))
	for index := range roots {
		target := createReferenceBatchTestPost(t, db, author.ID, "list-"+runID+"-target-"+strconvInt(index), "public", now.Add(-time.Minute), nil, nil)
		targetIDs = append(targetIDs, target.ID)
		quoteID := target.ID
		roots[index] = createReferenceBatchTestPost(t, db, author.ID, "list-"+runID+"-root-"+strconvInt(index), "public", now.Add(-time.Second), nil, &quoteID)
	}
	createdPostIDs := make([]uint, 0, 2*len(roots))
	rootIDs := make([]uint, 0, len(roots))
	for _, root := range roots {
		rootIDs = append(rootIDs, root.ID)
		createdPostIDs = append(createdPostIDs, root.ID)
	}
	createdPostIDs = append(createdPostIDs, targetIDs...)
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN ?", createdPostIDs).Delete(&models.PostMedia{})
		db.Unscoped().Where("id IN ?", rootIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", targetIDs).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", author.ID).Delete(&models.User{})
	})

	queryCountForPage := func(pageSize int) int {
		t.Helper()
		queryLogger := &postDetailSQLLogger{Interface: logger.Default}
		responses, err := loadPostResponses(db.Session(&gorm.Session{Logger: queryLogger}).Model(&models.Post{}).
			Where("posts.content LIKE ?", "list-"+runID+"-root-%").
			Order("posts.id ASC").Limit(pageSize))
		if err != nil {
			t.Fatal(err)
		}
		if len(responses) != pageSize {
			t.Fatalf("page size=%d returned %d responses", pageSize, len(responses))
		}
		assertPageBatchQueryCounts(t, queryLogger.snapshot(), 2, 2, 2)
		assertReferenceBatchPredicateSize(t, referencePostQueries(queryLogger.snapshot()), pageSize)
		return len(queryLogger.snapshot())
	}

	smallPageQueries := queryCountForPage(1)
	largePageQueries := queryCountForPage(20)
	if smallPageQueries != largePageQueries {
		t.Fatalf("reference-related query stages grew with page size: 1 item=%d queries, 20 items=%d queries", smallPageQueries, largePageQueries)
	}
}

func TestGetPostRepliesBatchesSharedParentReferenceIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for index := range 20 {
		createReplyRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "batch reply "+strconvInt(index), now.Add(-time.Duration(index)*time.Second))
	}

	queryLogger := &postDetailSQLLogger{Interface: logger.Default}
	originalDB := global.Db
	global.Db = db.Session(&gorm.Session{Logger: queryLogger})
	t.Cleanup(func() { global.Db = originalDB })
	ctx, recorder := newReplyIntegrationContext(http.MethodGet, "/api/posts/"+strconvUint(fixture.Article.ID)+"/replies?limit=50", strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetPostReplies(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response replyListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 20 {
		t.Fatalf("reply count=%d want 20", len(response.Items))
	}
	for _, reply := range response.Items {
		if reply.ReplyToPost == nil || reply.ReplyToPost.ID != fixture.Article.ID || reply.ReplyToPost.Deleted {
			t.Fatalf("reply parent=%#v", reply.ReplyToPost)
		}
	}
	assertReplyPageBatchQueryCounts(t, queryLogger.snapshot())
}

func createReferenceBatchTestPost(t *testing.T, db *gorm.DB, authorID uint, content, visibility string, createdAt time.Time, replyTo, quote *uint) models.Post {
	t.Helper()
	post := models.Post{
		Model:    gorm.Model{CreatedAt: createdAt, UpdatedAt: createdAt},
		AuthorID: authorID, Content: content, Language: "en", Visibility: visibility,
		ReplyToPostID: replyTo, QuotePostID: quote,
	}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	return post
}

func assertOneReferenceBatch(t *testing.T, queries []string, wantPosts, wantAuthors, wantMedia, wantReferenceIDs int) {
	t.Helper()
	posts := selectQueriesFromTable(queries, "posts")
	authors := selectQueriesFromTable(queries, "users")
	media := selectQueriesFromTable(queries, "post_media")
	if len(posts) != wantPosts || len(authors) != wantAuthors || len(media) != wantMedia {
		t.Fatalf("unexpected batch query counts: posts=%d authors=%d media=%d queries=%v", len(posts), len(authors), len(media), queries)
	}
	for _, query := range posts {
		if !strings.Contains(normalizeReferenceSQL(query), "posts.idin") {
			t.Fatalf("reference Posts should be fetched using a batched ID predicate: %s", query)
		}
		if strings.Contains(normalizeReferenceSQL(query), "posts.id=") {
			t.Fatalf("per-ID Post query detected: %s", query)
		}
	}
	assertReferenceBatchPredicateSize(t, posts, wantReferenceIDs)
	for _, query := range authors {
		if !strings.Contains(normalizeReferenceSQL(query), "selectid,username,display_name,avatar_urlfromusers") {
			t.Fatalf("author preload did not use the public projection: %s", query)
		}
	}
}

func assertPageBatchQueryCounts(t *testing.T, queries []string, wantPosts, wantAuthors, wantMedia int) {
	t.Helper()
	posts := selectQueriesFromTable(queries, "posts")
	if len(posts) != wantPosts || len(selectQueriesFromTable(queries, "users")) != wantAuthors || len(selectQueriesFromTable(queries, "post_media")) != wantMedia {
		t.Fatalf("page query counts grew beyond fixed hydration stages: posts=%d authors=%d media=%d queries=%v", len(posts), len(selectQueriesFromTable(queries, "users")), len(selectQueriesFromTable(queries, "post_media")), queries)
	}
	batchPosts := 0
	for _, query := range posts {
		if strings.Contains(normalizeReferenceSQL(query), "posts.idin") {
			batchPosts++
		}
	}
	if batchPosts != 1 {
		t.Fatalf("got %d batched reference Post queries, want 1: %v", batchPosts, posts)
	}
}

func assertReplyPageBatchQueryCounts(t *testing.T, queries []string) {
	t.Helper()
	posts := selectQueriesFromTable(queries, "posts")
	if len(posts) != 3 || len(selectQueriesFromTable(queries, "users")) != 2 || len(selectQueriesFromTable(queries, "post_media")) != 2 {
		t.Fatalf("reply page hydration query counts were not bounded: posts=%d authors=%d media=%d queries=%v", len(posts), len(selectQueriesFromTable(queries, "users")), len(selectQueriesFromTable(queries, "post_media")), queries)
	}
	batchPosts := 0
	for _, query := range posts {
		if strings.Contains(normalizeReferenceSQL(query), "posts.idin") {
			batchPosts++
		}
	}
	if batchPosts != 1 {
		t.Fatalf("got %d batched reference Post queries, want 1: %v", batchPosts, posts)
	}
	assertReferenceBatchPredicateSize(t, referencePostQueries(queries), 1)
}

func referencePostQueries(queries []string) []string {
	posts := selectQueriesFromTable(queries, "posts")
	batched := make([]string, 0, 1)
	for _, query := range posts {
		if strings.Contains(normalizeReferenceSQL(query), "posts.idin") {
			batched = append(batched, query)
		}
	}
	return batched
}

func assertReferenceBatchPredicateSize(t *testing.T, queries []string, wantIDs int) {
	t.Helper()
	if len(queries) != 1 {
		t.Fatalf("got %d batched reference Post predicates, want 1: %v", len(queries), queries)
	}
	normalized := normalizeReferenceSQL(queries[0])
	start := strings.Index(normalized, "posts.idin(")
	if start < 0 {
		t.Fatalf("reference query has no IN predicate: %s", queries[0])
	}
	values := normalized[start+len("posts.idin("):]
	end := strings.Index(values, ")")
	if end < 0 {
		t.Fatalf("reference IN predicate is incomplete: %s", queries[0])
	}
	values = values[:end]
	gotIDs := strings.Count(values, ",") + 1
	if values == "" {
		gotIDs = 0
	}
	if gotIDs != wantIDs {
		t.Fatalf("reference IN predicate has %d IDs, want %d: %s", gotIDs, wantIDs, queries[0])
	}
}

func selectQueriesFromTable(queries []string, table string) []string {
	selected := make([]string, 0)
	for _, query := range queries {
		normalized := normalizeReferenceSQL(query)
		fromIndex := strings.Index(normalized, "from")
		if fromIndex >= 0 && strings.HasPrefix(normalized[fromIndex:], "from"+table) {
			selected = append(selected, query)
		}
	}
	return selected
}

func normalizeReferenceSQL(query string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(strings.ToLower(query)), ""), `"`, "")
}

func strconvInt(value int) string {
	return strconv.Itoa(value)
}
