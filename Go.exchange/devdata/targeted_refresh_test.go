package devdata

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type targetedRefreshSourceClient struct {
	lookupHandles []string
	users         map[string]XUser
	posts         map[string][]XPost
}

func (client *targetedRefreshSourceClient) LookupUsers(_ context.Context, handles []string) (map[string]XUser, error) {
	client.lookupHandles = append(client.lookupHandles, handles...)
	users := make(map[string]XUser, len(handles))
	for _, handle := range handles {
		user, ok := client.users[strings.ToLower(handle)]
		if !ok {
			return users, errors.New("targeted fixture user is missing")
		}
		users[strings.ToLower(handle)] = user
	}
	return users, nil
}

func (client *targetedRefreshSourceClient) GetUserPosts(_ context.Context, sourceUserID, _ string, maxResults int) (XTimelinePage, error) {
	posts := append([]XPost(nil), client.posts[sourceUserID]...)
	if maxResults > 0 && len(posts) > maxResults {
		posts = posts[:maxResults]
	}
	return XTimelinePage{Posts: posts, ResultCount: len(posts)}, nil
}

func TestFetchTargetedAccountRequestsOnlySelectedHandle(t *testing.T) {
	registry := targetedRefreshTestRegistry()
	baseline := targetedRefreshBaseline(registry)
	client := targetedRefreshSourceClientFor(registry, targetedSourcePost("b", "200", "updated target post", time.Date(2026, 9, 21, 1, 0, 0, 0, time.UTC)))

	batch, report, err := FetchTargetedAccount(context.Background(), &client, registry, baseline, TargetedRefreshOptions{
		RegistryKey: "b",
		FetchCount:  20,
		FetchedAt:   time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.lookupHandles, []string{"b"}) {
		t.Fatalf("lookup handles=%v want [b]", client.lookupHandles)
	}
	if len(batch.Accounts) != 1 || batch.Accounts[0].RegistryKey != "b" {
		t.Fatalf("targeted accounts=%#v", batch.Accounts)
	}
	if report.RegistryKey != "b" || report.SourcePostsReturned != 1 {
		t.Fatalf("targeted report=%#v", report)
	}
}

func TestFetchTargetedAccountRejectsInvalidKeysBeforeSourceFetch(t *testing.T) {
	registry := targetedRefreshTestRegistry()
	registry.Accounts = append(registry.Accounts, SourceAccount{
		Key: "disabled", Platform: "x", Handle: "disabled", Category: "test", MaxPosts: 3,
	})
	baseline := targetedRefreshBaseline(registry)
	for _, key := range []string{"", "missing", "disabled"} {
		t.Run(keyOrEmptyLabel(key), func(t *testing.T) {
			client := targetedRefreshSourceClientFor(registry)
			_, _, err := FetchTargetedAccount(context.Background(), &client, registry, baseline, TargetedRefreshOptions{RegistryKey: key})
			if err == nil {
				t.Fatal("invalid key unexpectedly succeeded")
			}
			if len(client.lookupHandles) != 0 {
				t.Fatalf("source was queried for invalid key: %v", client.lookupHandles)
			}
		})
	}
}

func TestFetchTargetedAccountRequiresCompleteBaselineBeforeSourceFetch(t *testing.T) {
	registry := targetedRefreshTestRegistry()
	client := targetedRefreshSourceClientFor(registry)
	_, _, err := FetchTargetedAccount(context.Background(), &client, registry, Snapshot{}, TargetedRefreshOptions{RegistryKey: "b"})
	if err == nil || !errors.Is(err, ErrIncrementalBaselineRequired) {
		t.Fatalf("baseline error=%v", err)
	}
	if len(client.lookupHandles) != 0 {
		t.Fatalf("source was queried without a valid baseline: %v", client.lookupHandles)
	}
}

func TestMergeTargetedAccountPreservesUnrelatedAccountsAndPosts(t *testing.T) {
	registry := targetedRefreshTestRegistry()
	now := time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC)
	baseline := targetedRefreshBaseline(registry)
	updatedAccount := targetedSnapshotAccount("b", "updated B", "updated description", "https://img.example/b-new.jpg", true, "https://banner.example/b-new.jpg")
	updatedPost := targetedSnapshotPost("b", "200", "updated target post", now.Add(-time.Minute))
	newPost := targetedSnapshotPost("b", "203", "new target post", now)
	batch := TargetedRefreshBatch{
		RegistryKey: "b",
		FetchedAt:   now,
		Accounts:    []SnapshotAccount{updatedAccount},
		Posts:       []SnapshotPost{updatedPost, newPost},
	}
	beforeA := baselineAccountByKey(baseline, "a")
	beforeC := baselineAccountByKey(baseline, "c")
	beforeAPost := snapshotPostByKey(baseline, "a", "100")
	beforeCPost := snapshotPostByKey(baseline, "c", "300")

	next, err := MergeTargetedAccountSnapshot(baseline, batch, registry, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := baselineAccountByKey(next, "a"); !reflect.DeepEqual(got, beforeA) {
		t.Fatalf("unrelated account A changed: before=%#v after=%#v", beforeA, got)
	}
	if got := baselineAccountByKey(next, "c"); !reflect.DeepEqual(got, beforeC) {
		t.Fatalf("unrelated account C changed: before=%#v after=%#v", beforeC, got)
	}
	if got := snapshotPostByKey(next, "a", "100"); !reflect.DeepEqual(got, beforeAPost) {
		t.Fatalf("unrelated post A changed: before=%#v after=%#v", beforeAPost, got)
	}
	if got := snapshotPostByKey(next, "c", "300"); !reflect.DeepEqual(got, beforeCPost) {
		t.Fatalf("unrelated post C changed: before=%#v after=%#v", beforeCPost, got)
	}
	if got := baselineAccountByKey(next, "b"); !reflect.DeepEqual(got, updatedAccount) {
		t.Fatalf("target account=%#v want=%#v", got, updatedAccount)
	}
	if got := snapshotPostByKey(next, "b", "200"); got.Text != updatedPost.Text {
		t.Fatalf("target existing post=%#v", got)
	}
	if got := snapshotPostByKey(next, "b", "203"); got.Text != newPost.Text {
		t.Fatalf("target new post=%#v", got)
	}
}

func TestMergeTargetedAccountRejectsSourceIdentityChange(t *testing.T) {
	registry := targetedRefreshTestRegistry()
	baseline := targetedRefreshBaseline(registry)
	changed := targetedSnapshotAccount("b", "changed identity", "description", "https://img.example/b.jpg", false, "")
	changed.SourceUserID = "999"
	batch := TargetedRefreshBatch{
		RegistryKey: "b",
		FetchedAt:   time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC),
		Accounts:    []SnapshotAccount{changed},
	}
	_, err := MergeTargetedAccountSnapshot(baseline, batch, registry, time.Time{})
	if err == nil || !errors.Is(err, ErrSourceIdentityMismatch) {
		t.Fatalf("identity change error=%v", err)
	}
}

func targetedRefreshTestRegistry() SourceRegistry {
	return SourceRegistry{
		Version:         SourceRegistryVersion,
		DefaultMaxPosts: 3,
		Accounts: []SourceAccount{
			{Key: "a", Platform: "x", Handle: "a", Category: "test", MaxPosts: 3, Enabled: true},
			{Key: "b", Platform: "x", Handle: "b", Category: "test", MaxPosts: 3, Enabled: true},
			{Key: "c", Platform: "x", Handle: "c", Category: "test", MaxPosts: 3, Enabled: true},
		},
	}
}

func targetedRefreshBaseline(registry SourceRegistry) Snapshot {
	return Snapshot{
		Version:   DefaultSnapshotVersion,
		FetchedAt: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC),
		Accounts: []SnapshotAccount{
			targetedSnapshotAccount("a", "A", "A description", "https://img.example/a.jpg", false, ""),
			targetedSnapshotAccount("b", "B", "B description", "https://img.example/b.jpg", false, ""),
			targetedSnapshotAccount("c", "C", "C description", "https://img.example/c.jpg", true, "https://banner.example/c.jpg"),
		},
		Posts: []SnapshotPost{
			targetedSnapshotPost("a", "100", "A baseline post", time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)),
			targetedSnapshotPost("b", "200", "B baseline post", time.Date(2026, 9, 19, 1, 0, 0, 0, time.UTC)),
			targetedSnapshotPost("c", "300", "C baseline post", time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC)),
		},
	}
}

func targetedRefreshSourceClientFor(registry SourceRegistry, posts ...XPost) targetedRefreshSourceClient {
	users := make(map[string]XUser, len(registry.Accounts))
	postByID := make(map[string][]XPost)
	for index, account := range registry.EnabledAccounts() {
		protected := false
		id := string(rune('1' + index))
		users[account.Handle] = XUser{ID: id, Name: strings.ToUpper(account.Handle), Username: account.Handle, Description: "source description", Protected: &protected}
		postByID[id] = nil
	}
	for _, post := range posts {
		postByID[post.AuthorID] = append(postByID[post.AuthorID], post)
	}
	return targetedRefreshSourceClient{users: users, posts: postByID}
}

func targetedSourcePost(handle, id, text string, createdAt time.Time) XPost {
	return XPost{ID: id, AuthorID: string(rune('1' + (handle[0] - 'a'))), Text: text, CreatedAt: createdAt, Lang: "en"}
}

func targetedSnapshotAccount(key, name, description, imageURL string, bannerPresent bool, bannerURL string) SnapshotAccount {
	return SnapshotAccount{
		RegistryKey: key, SourceUserID: string(rune('1' + (key[0] - 'a'))), Handle: key,
		Name: name, Description: description, ProfileImageURL: imageURL,
		ProfileBannerPresent: bannerPresent, ProfileBannerURL: bannerURL, Category: "test",
	}
}

func targetedSnapshotPost(key, id, text string, createdAt time.Time) SnapshotPost {
	return SnapshotPost{
		RegistryKey: key, SourcePostID: id, SourceURL: "https://x.com/" + key + "/status/" + id,
		Text: text, CreatedAt: createdAt.UTC(), Language: "und",
	}
}

func baselineAccountByKey(snapshot Snapshot, key string) SnapshotAccount {
	for _, account := range snapshot.Accounts {
		if account.RegistryKey == key {
			return account
		}
	}
	return SnapshotAccount{}
}

func snapshotPostByKey(snapshot Snapshot, accountKey, postID string) SnapshotPost {
	for _, post := range snapshot.Posts {
		if post.RegistryKey == accountKey && post.SourcePostID == postID {
			return post
		}
	}
	return SnapshotPost{}
}

func keyOrEmptyLabel(key string) string {
	if key == "" {
		return "empty"
	}
	return key
}
