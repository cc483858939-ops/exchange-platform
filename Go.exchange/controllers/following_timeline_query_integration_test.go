package controllers

import (
	"fmt"
	"sort"
	"testing"
	"time"
)

func TestFollowingFirstPageMatchesCanonicalActivityOrder(t *testing.T) {
	db := openFollowingTimelineIntegrationDatabase(t)
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	for _, query := range []string{
		`CREATE TEMP TABLE users (id bigint PRIMARY KEY, deleted_at timestamptz)`,
		`CREATE TEMP TABLE posts (id bigint PRIMARY KEY, author_id bigint, created_at timestamptz, deleted_at timestamptz, visibility text, reply_to_post_id bigint)`,
		`CREATE TEMP TABLE user_follows (follower_id bigint, following_id bigint, PRIMARY KEY(follower_id, following_id))`,
		`CREATE TEMP TABLE post_reposts (id bigint PRIMARY KEY, user_id bigint, post_id bigint, created_at timestamptz, UNIQUE(user_id, post_id))`,
		`CREATE INDEX ON posts (author_id, created_at DESC, id DESC) WHERE deleted_at IS NULL`,
		`CREATE INDEX ON post_reposts (user_id, created_at DESC, id DESC)`,
		`INSERT INTO users SELECT id, CASE WHEN id IN (7,8) THEN '2026-01-01'::timestamptz END FROM generate_series(1,12) id`,
		`INSERT INTO user_follows SELECT 9000, id FROM generate_series(1,8) id`,
		`INSERT INTO posts SELECT id, 1+(id-1)%12, '2026-01-01'::timestamptz+(id%29)*interval '1 second',
            CASE WHEN id%13=0 THEN '2026-01-01'::timestamptz END,
            CASE WHEN id%11=0 THEN 'private' ELSE 'public' END,
            CASE WHEN id%7=0 THEN 1 END FROM generate_series(1,400) id`,
		`INSERT INTO post_reposts SELECT (actor-1)*400+post, actor, post,
            '2026-01-01'::timestamptz+((post+actor*3)%29)*interval '1 second'
            FROM generate_series(1,10) actor CROSS JOIN generate_series(1,400) post WHERE post%3<>0`,
	} {
		if err := tx.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := func(a, b timelineActivityQueryRow) bool {
		if !a.ActivityAt.Equal(b.ActivityAt) {
			return a.ActivityAt.After(b.ActivityAt)
		}
		if a.ActivityRank != b.ActivityRank {
			return a.ActivityRank > b.ActivityRank
		}
		return a.SourceID > b.SourceID
	}
	winners := map[uint]timelineActivityQueryRow{}
	for postID := uint(1); postID <= 400; postID++ {
		authorID := 1 + (postID-1)%12
		if postID%13 == 0 || postID%11 == 0 || authorID == 7 || authorID == 8 {
			continue
		}
		if authorID <= 8 && postID%7 != 0 {
			winners[postID] = timelineActivityQueryRow{
				ActivityType: string(timelineActivityPost), ActivityAt: base.Add(time.Duration(postID%29) * time.Second),
				SourceID: postID, PostID: postID, ActorID: authorID, ActivityRank: 1,
			}
		}
		if postID%3 == 0 {
			continue
		}
		for actorID := uint(1); actorID <= 6; actorID++ {
			candidate := timelineActivityQueryRow{
				ActivityType: string(timelineActivityRepost),
				ActivityAt:   base.Add(time.Duration((postID+actorID*3)%29) * time.Second),
				SourceID:     (actorID-1)*400 + postID, PostID: postID, ActorID: actorID, ActivityRank: 2,
			}
			if current, ok := winners[postID]; !ok || newer(candidate, current) {
				winners[postID] = candidate
			}
		}
	}
	want := make([]timelineActivityQueryRow, 0, len(winners))
	for _, winner := range winners {
		want = append(want, winner)
	}
	sort.Slice(want, func(i, j int) bool { return newer(want[i], want[j]) })
	for _, limit := range []int{1, 2, 20, 100, 500} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			var got []timelineActivityQueryRow
			if err := tx.Raw(followingTimelineFirstPageSQL(), 9000, limit+1, limit+1, limit+1).Scan(&got).Error; err != nil {
				t.Fatal(err)
			}
			count := min(limit+1, len(want))
			if len(got) != count {
				t.Fatalf("got %d activities, want %d", len(got), count)
			}
			for i, actual := range got {
				expected := want[i]
				if actual.ActivityType != expected.ActivityType || !actual.ActivityAt.Equal(expected.ActivityAt) ||
					actual.SourceID != expected.SourceID || actual.PostID != expected.PostID ||
					actual.ActorID != expected.ActorID || actual.ActivityRank != expected.ActivityRank {
					t.Fatalf("activity %d: got %+v, want %+v", i, actual, expected)
				}
			}
		})
	}
	cursorPositions := []int{0, 1, 19, 100, len(want) - 1}
	for i, winner := range want {
		if winner.ActivityType == string(timelineActivityPost) {
			cursorPositions = append(cursorPositions, i)
			break
		}
	}
	for _, position := range cursorPositions {
		t.Run(fmt.Sprintf("cursor_%d", position), func(t *testing.T) {
			cursor := want[position]
			var got []timelineActivityQueryRow
			if err := tx.Raw(followingTimelineCursorSQL(), 9000, 9000,
				cursor.ActivityAt, cursor.ActivityAt, cursor.ActivityRank, cursor.ActivityRank, cursor.SourceID, 21,
			).Scan(&got).Error; err != nil {
				t.Fatal(err)
			}
			tail := want[position+1:]
			if len(got) != min(21, len(tail)) {
				t.Fatalf("got %d activities, want %d", len(got), min(21, len(tail)))
			}
			for i, actual := range got {
				expected := tail[i]
				if actual.ActivityType != expected.ActivityType || !actual.ActivityAt.Equal(expected.ActivityAt) ||
					actual.SourceID != expected.SourceID || actual.PostID != expected.PostID ||
					actual.ActorID != expected.ActorID || actual.ActivityRank != expected.ActivityRank {
					t.Fatalf("activity %d: got %+v, want %+v", i, actual, expected)
				}
			}
		})
	}
	var empty []timelineActivityQueryRow
	if err := tx.Raw(followingTimelineFirstPageSQL(), 9001, 21, 21, 21).Scan(&empty).Error; err != nil || len(empty) != 0 {
		t.Fatalf("unfollowed viewer: rows=%v err=%v", empty, err)
	}
	if err := tx.Exec(`INSERT INTO posts VALUES
        (401,1,NULL,NULL,'public',NULL), (402,1,'2026-01-01',NULL,'public',NULL);
        INSERT INTO post_reposts VALUES
        (6001,1,401,'2026-01-01'), (6002,1,402,NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	var older []timelineActivityQueryRow
	if err := tx.Raw(followingTimelineCursorSQL(), 9000, 9000,
		base.Add(time.Hour), base.Add(time.Hour), 2, 2, 99999, 500,
	).Scan(&older).Error; err != nil {
		t.Fatal(err)
	}
	for _, activity := range older {
		if activity.PostID == 401 || activity.PostID == 402 {
			t.Fatalf("null latest activity must not reveal an older representative: %+v", activity)
		}
	}
}
