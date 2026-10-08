package controllers

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestTimelineMissingCandidatesAdvancePastScannedRows(t *testing.T) {
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	rows := []timelineActivityQueryRow{
		{ActivityType: "repost", ActivityAt: at, SourceID: 3, PostID: 13, ActorID: 23},
		{ActivityType: "post", ActivityAt: at, SourceID: 12, PostID: 12, ActorID: 22},
		{ActivityType: "post", ActivityAt: at, SourceID: 11, PostID: 11, ActorID: 21},
	}
	for _, missing := range []string{"post", "actor", "last-post", "last-actor", "all"} {
		t.Run(missing, func(t *testing.T) {
			posts := map[uint]postResponse{13: {ID: 13}, 12: {ID: 12}, 11: {ID: 11}}
			actors := map[uint]publicAuthorResponse{23: {ID: 23}, 22: {ID: 22}, 21: {ID: 21}}
			switch missing {
			case "post":
				delete(posts, 13)
			case "actor":
				delete(actors, 23)
			case "last-post":
				delete(posts, 12)
			case "last-actor":
				delete(actors, 22)
			case "all":
				posts = nil
			}
			page, err := buildTimelinePageResponse(rows, posts, actors, 2)
			if err != nil || page.NextCursor == nil || page.Items == nil {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			wantItems := 1
			if missing == "all" {
				wantItems = 0
			}
			if len(page.Items) != wantItems {
				t.Fatalf("items=%+v want length=%d", page.Items, wantItems)
			}
			if wantItems == 1 {
				wantID := uint(12)
				if missing == "last-post" || missing == "last-actor" {
					wantID = 13
				}
				if page.Items[0].Post.ID != wantID {
					t.Fatalf("remaining post=%d want=%d", page.Items[0].Post.ID, wantID)
				}
			}
			cursor, err := decodeTimelineCursor(*page.NextCursor)
			if err != nil || cursor.SourceID != 12 || cursor.ActivityType != "post" || !cursor.ActivityAt.Equal(at) {
				t.Fatalf("cursor=%+v err=%v", cursor, err)
			}
			// The lookahead was not consumed to fill the gap, so the next page
			// still returns it exactly once, including after an empty page.
			next, err := buildTimelinePageResponse(rows[2:], posts, actors, 2)
			wantNextItems := 1
			if missing == "all" {
				wantNextItems = 0
			}
			if err != nil || len(next.Items) != wantNextItems || next.NextCursor != nil {
				t.Fatalf("next page=%+v err=%v", next, err)
			}
			if wantNextItems == 1 && next.Items[0].Post.ID != 11 {
				t.Fatalf("lookahead post=%d want 11", next.Items[0].Post.ID)
			}
			final, err := buildTimelinePageResponse(rows[:2], posts, actors, 2)
			if err != nil || final.NextCursor != nil {
				t.Fatalf("exhausted candidates page=%+v err=%v", final, err)
			}
		})
	}
}

func TestTimelineMissingCandidatesDoNotHideInvalidActivities(t *testing.T) {
	valid := timelineActivityQueryRow{ActivityType: "post", ActivityAt: time.Now().UTC(), SourceID: 10, PostID: 10, ActorID: 7}
	for _, field := range []string{"type", "time", "source", "post", "actor", "post-identity", "actor-identity"} {
		t.Run(field, func(t *testing.T) {
			row := valid
			posts := map[uint]postResponse{}
			actors := map[uint]publicAuthorResponse{}
			switch field {
			case "type":
				row.ActivityType = "unknown"
			case "time":
				row.ActivityAt = time.Time{}
			case "source":
				row.SourceID = 0
			case "post":
				row.PostID = 0
			case "actor":
				row.ActorID = 0
			case "post-identity":
				posts[10] = postResponse{ID: 99}
				actors[7] = publicAuthorResponse{ID: 7}
			case "actor-identity":
				posts[10] = postResponse{ID: 10}
				actors[7] = publicAuthorResponse{ID: 99}
			}
			if _, err := buildTimelinePageResponse([]timelineActivityQueryRow{row}, posts, actors, 1); err == nil {
				t.Fatal("invalid timeline identity/activity was silently skipped")
			}
		})
	}
}

type timelineMissingFixture struct {
	missingPosts   map[uint]bool
	missingAuthor  bool
	missingActor   bool
	zeroAuthorID   bool
	failQuery      string
	err            error
	candidateReads int
}

func (fixture *timelineMissingFixture) install(t *testing.T) {
	t.Helper()
	originalDB := global.APIDb
	originalActive, originalUser := loadActiveFollowingViewer, loadUserTimelineUser
	originalFollowing, originalProfile := loadFollowingTimelinePage, loadUserTimelinePage
	t.Cleanup(func() {
		global.APIDb = originalDB
		loadActiveFollowingViewer, loadUserTimelineUser = originalActive, originalUser
		loadFollowingTimelinePage, loadUserTimelinePage = originalFollowing, originalProfile
	})
	loadActiveFollowingViewer = func(context.Context, uint) error { return nil }
	loadUserTimelineUser = func(context.Context, uint) error { return nil }
	loadFollowingTimelinePage, loadUserTimelinePage = loadFollowingTimelinePageFromDB, loadUserTimelinePageFromDB
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	global.APIDb = visibilityRefreshDB(t, func(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		var stage string
		switch {
		case strings.Contains(query, "activities AS (") || strings.Contains(query, "WITH reposts AS ("):
			stage = "candidates"
		case strings.Contains(query, `FROM "posts"`):
			stage = "posts"
		case strings.Contains(query, `FROM "users"`):
			stage = "actor"
			if len(args) == 1 && args[0].Value == int64(8) {
				stage = "author"
			}
		case strings.Contains(query, `FROM "post_media"`):
			stage = "media"
		case strings.Contains(query, "post_reposts"):
			stage = "reposts"
		default:
			return nil, errors.New("unexpected timeline query: " + query)
		}
		if stage == fixture.failQuery {
			return nil, fixture.err
		}
		switch stage {
		case "candidates":
			fixture.candidateReads++
			if !strings.Contains(query, "LIMIT $") || strings.Contains(query, "OFFSET") || args[len(args)-1].Value != int64(3) {
				t.Fatalf("unbounded candidate query: %s args=%v", query, args)
			}
			rows := &visibilityRefreshRows{columns: []string{"activity_type", "activity_at", "source_id", "post_id", "actor_id", "activity_rank"}}
			for sourceID := int64(206); sourceID >= 201 && len(rows.values) < 3; sourceID-- {
				if len(args) == 8 && sourceID >= args[6].Value.(int64) {
					continue
				}
				rows.values = append(rows.values, []driver.Value{"repost", at, sourceID, sourceID - 100, int64(7), int64(2)})
			}
			return rows, nil
		case "posts":
			for _, predicate := range []string{"posts.deleted_at IS NULL", "posts.visibility = 'public'", "post_author.deleted_at IS NULL"} {
				if !strings.Contains(query, predicate) {
					t.Fatalf("missing eligibility predicate %q: %s", predicate, query)
				}
			}
			rows := &visibilityRefreshRows{columns: []string{"id", "author_id", "content", "visibility", "created_at", "updated_at"}}
			for _, arg := range args {
				id := uint(arg.Value.(int64))
				if fixture.missingPosts[id] {
					continue
				}
				authorID := int64(8)
				if fixture.zeroAuthorID {
					authorID = 0
				}
				rows.values = append(rows.values, []driver.Value{int64(id), authorID, "timeline body", "public", at, at})
			}
			return rows, nil
		case "author", "actor":
			rows := &visibilityRefreshRows{columns: []string{"id", "username"}}
			if (stage == "author" && !fixture.missingAuthor) || (stage == "actor" && !fixture.missingActor) {
				rows.values = [][]driver.Value{{args[0].Value, "active-user"}}
			}
			return rows, nil
		case "media":
			return &visibilityRefreshRows{columns: []string{"post_id"}}, nil
		default:
			return &visibilityRefreshRows{columns: []string{"post_id", "reposts"}}, nil
		}
	})
}

func timelineMissingRequest(t *testing.T, endpoint, query string) (timelinePageResponse, int, string) {
	t.Helper()
	var status int
	var body string
	if endpoint == "following" {
		viewerID := uint(42)
		ctx, recorder := newFollowingTimelineTestContext("/api/feed/following?"+query, &viewerID)
		GetFollowingTimeline(ctx)
		status, body = recorder.Code, recorder.Body.String()
	} else {
		ctx, recorder := newUserControllerContext("/api/users/7/timeline?"+query, "7")
		GetUserTimeline(ctx)
		status, body = recorder.Code, recorder.Body.String()
	}
	var page timelinePageResponse
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatal(err)
	}
	return page, status, body
}

func TestTimelineMissingCandidatesThroughRealLoaders(t *testing.T) {
	for _, endpoint := range []string{"following", "profile"} {
		for _, missing := range []string{"post", "last-post", "all-posts", "actor", "author-preload", "stable"} {
			t.Run(endpoint+"/"+missing, func(t *testing.T) {
				fixture := timelineMissingFixture{missingPosts: map[uint]bool{}}
				wantItems := 1
				switch missing {
				case "post":
					fixture.missingPosts[106] = true
				case "last-post":
					fixture.missingPosts[105] = true
				case "all-posts":
					fixture.missingPosts = map[uint]bool{106: true, 105: true, 104: true}
					wantItems = 0
				case "actor":
					fixture.missingActor, wantItems = true, 0
				case "author-preload":
					fixture.missingAuthor, wantItems = true, 0
				case "stable":
					wantItems = 2
				}
				fixture.install(t)
				page, status, body := timelineMissingRequest(t, endpoint, "limit=2")
				if status != http.StatusOK || page.Items == nil || len(page.Items) != wantItems || page.NextCursor == nil || fixture.candidateReads != 1 {
					t.Fatalf("status=%d body=%s candidate reads=%d", status, body, fixture.candidateReads)
				}
				cursor, err := decodeTimelineCursor(*page.NextCursor)
				if err != nil || cursor.SourceID != 205 {
					t.Fatalf("cursor=%+v err=%v", cursor, err)
				}
				if missing != "actor" && missing != "author-preload" {
					// Retry stays at the same boundary. Next-page SQL uses that
					// boundary, preserving the lookahead and never repeating it.
					retry, status, body := timelineMissingRequest(t, endpoint, "limit=2")
					if status != http.StatusOK || retry.NextCursor == nil || *retry.NextCursor != *page.NextCursor {
						t.Fatalf("retry status=%d body=%s", status, body)
					}
					page2, status, body := timelineMissingRequest(t, endpoint, "limit=2&cursor="+*page.NextCursor)
					wantNextItems := 2
					if missing == "all-posts" {
						wantNextItems = 1
					}
					if status != http.StatusOK || page2.NextCursor == nil || len(page2.Items) != wantNextItems || page2.Items[len(page2.Items)-1].Post.ID != 103 {
						t.Fatalf("page2 status=%d body=%s", status, body)
					}
					if wantNextItems == 2 && page2.Items[0].Post.ID != 104 {
						t.Fatalf("lookahead post=%d want 104", page2.Items[0].Post.ID)
					}
					page3, status, body := timelineMissingRequest(t, endpoint, "limit=2&cursor="+*page2.NextCursor)
					if status != http.StatusOK || page3.NextCursor != nil || len(page3.Items) != 2 || page3.Items[0].Post.ID != 102 || page3.Items[1].Post.ID != 101 {
						t.Fatalf("page3 status=%d body=%s", status, body)
					}
				}
			})
		}
	}
}

func TestTimelineMissingCandidatesKeepQueryErrorsAndInvalidAuthors(t *testing.T) {
	for _, endpoint := range []string{"following", "profile"} {
		for _, stage := range []string{"candidates", "posts", "author", "actor", "media", "reposts", "invalid-author"} {
			t.Run(endpoint+"/"+stage, func(t *testing.T) {
				fixture := timelineMissingFixture{failQuery: stage, err: errors.New("private query failure"), zeroAuthorID: stage == "invalid-author"}
				fixture.install(t)
				_, status, body := timelineMissingRequest(t, endpoint, "limit=2")
				if status != http.StatusInternalServerError || strings.Contains(body, "private query failure") {
					t.Fatalf("status=%d body=%s", status, body)
				}
			})
		}
		// Existing timeout classification must survive the missing-row policy.
		t.Run(endpoint+"/deadline", func(t *testing.T) {
			fixture := timelineMissingFixture{failQuery: "actor", err: context.DeadlineExceeded}
			fixture.install(t)
			_, status, body := timelineMissingRequest(t, endpoint, "limit=2")
			if status != http.StatusGatewayTimeout {
				t.Fatalf("status=%d body=%s", status, body)
			}
		})
	}
	// Other list/detail callers retain their strict author contract.
	t.Run("strict-loader", func(t *testing.T) {
		fixture := timelineMissingFixture{missingAuthor: true}
		fixture.install(t)
		if _, err := loadPostResponses(global.APIDb.Model(&models.Post{}).Where("posts.id IN ?", []uint{106}).Scopes(func(db *gorm.DB) *gorm.DB {
			return publicPostScope(db, time.Now().UTC())
		})); err == nil {
			t.Fatal("strict post loader silently skipped a missing author")
		}
	})
}
