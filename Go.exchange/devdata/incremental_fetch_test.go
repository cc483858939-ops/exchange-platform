package devdata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type incrementalWindowClient struct {
	posts       []XPost
	returnCount int
	calls       []int
	failHandle  string
	lookupErr   error
}

func (client *incrementalWindowClient) LookupUsers(_ context.Context, handles []string) (map[string]XUser, error) {
	users := make(map[string]XUser, len(handles))
	for index, handle := range handles {
		if client.failHandle != "" && strings.EqualFold(handle, client.failHandle) {
			if client.lookupErr != nil {
				return users, client.lookupErr
			}
			return users, errors.New("fixture lookup failure")
		}
		protected := false
		users[strings.ToLower(handle)] = XUser{
			ID: "700" + string(rune('0'+index)), Name: "Source", Username: handle, Protected: &protected,
		}
	}
	return users, nil
}

func (client *incrementalWindowClient) GetUserPosts(_ context.Context, _ string, _ string, maxResults int) (XTimelinePage, error) {
	client.calls = append(client.calls, maxResults)
	count := len(client.posts)
	if maxResults > 0 && count > maxResults {
		count = maxResults
	}
	if client.returnCount >= 0 && client.returnCount < count {
		count = client.returnCount
	}
	return XTimelinePage{Posts: append([]XPost(nil), client.posts[:count]...), ResultCount: count}, nil
}

func incrementalWindowPosts() []XPost {
	posts := make([]XPost, 60)
	for index := range posts {
		posts[index] = XPost{
			ID:        fmt.Sprintf("200000000000000%03d", index),
			AuthorID:  "7000",
			Text:      "valid incremental source post " + string(rune('a'+index)),
			CreatedAt: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC).Add(-time.Duration(index) * time.Minute),
		}
	}
	return posts
}

func incrementalBaseline(registry SourceRegistry, posts ...SnapshotPost) Snapshot {
	accounts := make([]SnapshotAccount, 0, len(registry.Accounts))
	for index, account := range registry.EnabledAccounts() {
		accounts = append(accounts, SnapshotAccount{
			RegistryKey: account.Key, SourceUserID: "700" + string(rune('0'+index)), Handle: account.Handle,
			Name: "Source", Category: account.Category,
		})
	}
	return Snapshot{Version: DefaultSnapshotVersion, FetchedAt: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC), Accounts: accounts, Posts: posts}
}

func incrementalSnapshotPost(account SourceAccount, id string, at time.Time) SnapshotPost {
	return SnapshotPost{
		RegistryKey: account.Key, SourcePostID: id, SourceURL: "https://x.com/" + account.Handle + "/status/" + id,
		Text: "baseline post content", CreatedAt: at.UTC(), Language: "und",
	}
}

func TestFetchIncrementalBatchEscalatesOnlyWhenTwentyWindowHasNoBaselineOverlap(t *testing.T) {
	registry := incrementalTestRegistry(4)
	assignments, err := BuildIncrementalShardAssignments(registry)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := SelectIncrementalShardAccounts(registry, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 {
		t.Fatalf("selected=%d want one account", len(selected))
	}
	target := selected[0]
	baseline := incrementalBaseline(registry)
	client := &incrementalWindowClient{posts: incrementalWindowPosts(), returnCount: -1}
	batch, report, err := FetchIncrementalBatch(context.Background(), client, registry, baseline, assignments[target.Key], 20, time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 2 || client.calls[0] != 20 || client.calls[1] != 60 {
		t.Fatalf("source calls=%v want [20 60]", client.calls)
	}
	if report.EscalatedToFull != 1 || report.CoverageWindowExhausted != 1 {
		t.Fatalf("report=%#v", report)
	}
	if len(batch.Accounts) != 1 || len(batch.Posts) != 40 {
		t.Fatalf("batch accounts=%d posts=%d", len(batch.Accounts), len(batch.Posts))
	}
}

func TestFetchIncrementalBatchCoverageBoundaries(t *testing.T) {
	registry := incrementalTestRegistry(4)
	assignments, err := BuildIncrementalShardAssignments(registry)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := SelectIncrementalShardAccounts(registry, 0)
	if err != nil || len(selected) != 1 {
		t.Fatalf("selected=%v err=%v", selected, err)
	}
	target := selected[0]
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	cases := []struct {
		name          string
		baselinePosts []SnapshotPost
		returnCount   int
		fetchCount    int
		wantCalls     []int
		wantEscalate  int
	}{
		{name: "overlap", baselinePosts: []SnapshotPost{incrementalSnapshotPost(target, incrementalWindowPosts()[0].ID, now)}, returnCount: -1, fetchCount: 20, wantCalls: []int{20}, wantEscalate: 0},
		{name: "short response", returnCount: 3, fetchCount: 20, wantCalls: []int{20}, wantEscalate: 0},
		{name: "configured sixty", returnCount: -1, fetchCount: 60, wantCalls: []int{60}, wantEscalate: 0},
		{name: "empty baseline saturated", baselinePosts: []SnapshotPost{}, returnCount: -1, fetchCount: 20, wantCalls: []int{20, 60}, wantEscalate: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			baseline := incrementalBaseline(registry, test.baselinePosts...)
			client := &incrementalWindowClient{posts: incrementalWindowPosts(), returnCount: test.returnCount}
			_, report, err := FetchIncrementalBatch(context.Background(), client, registry, baseline, assignments[target.Key], test.fetchCount, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(client.calls) != len(test.wantCalls) {
				t.Fatalf("calls=%v want=%v", client.calls, test.wantCalls)
			}
			for index, want := range test.wantCalls {
				if client.calls[index] != want {
					t.Fatalf("calls=%v want=%v", client.calls, test.wantCalls)
				}
			}
			if report.EscalatedToFull != test.wantEscalate {
				t.Fatalf("escalated=%d want=%d report=%#v", report.EscalatedToFull, test.wantEscalate, report)
			}
		})
	}
}

func TestFetchIncrementalBatchNeverReturnsPartialShardAfterSourceFailure(t *testing.T) {
	registry := incrementalTestRegistry(8)
	shard := 0
	selected, err := SelectIncrementalShardAccounts(registry, shard)
	if err != nil || len(selected) < 2 {
		t.Fatalf("selected=%v err=%v", selected, err)
	}
	client := &incrementalWindowClient{
		posts:       incrementalWindowPosts(),
		returnCount: -1,
		failHandle:  selected[1].Handle,
		lookupErr:   errors.New("fixture source unavailable"),
	}
	batch, _, err := FetchIncrementalBatch(context.Background(), client, registry, incrementalBaseline(registry), shard, 20, time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "fixture source unavailable") {
		t.Fatalf("source failure error=%v", err)
	}
	if len(batch.Accounts) != 0 || len(batch.Posts) != 0 {
		t.Fatalf("partial batch returned after source failure: %#v", batch)
	}
}
