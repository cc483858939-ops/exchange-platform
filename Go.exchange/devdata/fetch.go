package devdata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SnapshotSourceClient is the small source contract needed by the snapshot
// pipeline. Both the official X API client and the RSSHub adapter implement it,
// so fetching and desired-state sync remain source-agnostic.
type SnapshotSourceClient interface {
	LookupUsers(ctx context.Context, handles []string) (map[string]XUser, error)
	GetUserPosts(ctx context.Context, sourceUserID, paginationToken string, maxResults int) (XTimelinePage, error)
}

type sourceRequestCounter interface {
	RequestCount() int
}

func sourceRequestCount(client SnapshotSourceClient) int {
	if counter, ok := client.(sourceRequestCounter); ok && counter.RequestCount() > 0 {
		return counter.RequestCount()
	}
	return 1
}

func FetchSnapshot(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry, fetchedAt time.Time) (Snapshot, FetchReport, error) {
	if err := ValidateRegistry(registry); err != nil {
		return Snapshot{}, FetchReport{}, err
	}
	if client == nil {
		return Snapshot{}, FetchReport{}, errors.New("source client is not initialized")
	}
	if fetchedAt.IsZero() {
		fetchedAt = time.Now().UTC()
	}
	accounts := registry.EnabledAccounts()
	users, lookupErr := client.LookupUsers(ctx, registryHandles(accounts))
	report := FetchReport{APIRequests: sourceRequestCount(client), PerAccount: make([]FetchAccountReport, 0, len(accounts))}
	if lookupErr != nil {
		return Snapshot{}, report, fmt.Errorf("lookup source accounts: %w", lookupErr)
	}
	snapshot := Snapshot{
		Version:   DefaultSnapshotVersion,
		FetchedAt: fetchedAt.UTC(),
		Accounts:  make([]SnapshotAccount, 0, len(accounts)),
		Posts:     make([]SnapshotPost, 0, len(accounts)*DefaultMaxPosts),
	}
	for _, account := range accounts {
		user, exists := users[strings.ToLower(account.Handle)]
		if !exists {
			return Snapshot{}, report, fmt.Errorf("source account %q was not returned by source", account.Key)
		}
		data, accountErr := fetchSnapshotAccountWithUser(ctx, client, account, user)
		if accountErr != nil {
			return Snapshot{}, report, accountErr
		}
		snapshot.Accounts = append(snapshot.Accounts, data.Account)
		report.PerAccount = append(report.PerAccount, data.Report)
		report.SourcePostsScanned += data.Report.SourcePostsScanned
		report.EligibleSelected += data.Report.EligibleSelected
		if _, counted := client.(sourceRequestCounter); counted {
			report.APIRequests = sourceRequestCount(client)
		} else {
			report.APIRequests += data.Report.APIRequests
		}
		snapshot.Posts = append(snapshot.Posts, data.Posts...)
	}
	sortSnapshotAccounts(snapshot.Accounts)
	sortSnapshotPosts(snapshot.Posts)
	if err := ValidateSnapshot(snapshot, registry); err != nil {
		return Snapshot{}, report, err
	}
	return snapshot, report, nil
}

// FetchSnapshotAccount fetches and validates one configured account. It is the
// independently checkpointable unit used by the RSSHub batch runner.
func FetchSnapshotAccount(ctx context.Context, client SnapshotSourceClient, account SourceAccount) (FetchAccountData, error) {
	if client == nil {
		return FetchAccountData{}, errors.New("source client is not initialized")
	}
	users, err := client.LookupUsers(ctx, []string{account.Handle})
	if err != nil {
		return FetchAccountData{}, fmt.Errorf("lookup source account %q: %w", account.Key, err)
	}
	user, exists := users[strings.ToLower(account.Handle)]
	if !exists {
		return FetchAccountData{}, fmt.Errorf("source account %q was not returned by source", account.Key)
	}
	data, err := fetchSnapshotAccountWithUser(ctx, client, account, user)
	if err != nil {
		return FetchAccountData{}, err
	}
	data.Report.APIRequests++
	return data, nil
}

func fetchSnapshotAccountWithUser(ctx context.Context, client SnapshotSourceClient, account SourceAccount, user XUser) (FetchAccountData, error) {
	snapshotAccount, err := validateSourceUser(account, user)
	if err != nil {
		return FetchAccountData{}, err
	}
	accountReport := FetchAccountReport{RegistryKey: account.Key}
	selected := make([]SnapshotPost, 0, account.MaxPosts)
	resolvedAccount := account
	resolvedAccount.Handle = snapshotAccount.Handle
	nextToken := ""
	for accountReport.SourcePostsScanned < DefaultMaxScanned && len(selected) < account.MaxPosts {
		page, pageErr := client.GetUserPosts(ctx, user.ID, nextToken, 100)
		accountReport.APIRequests++
		if pageErr != nil {
			return FetchAccountData{}, fmt.Errorf("fetch source Posts for %q: %w", account.Key, pageErr)
		}
		for _, post := range page.Posts {
			if accountReport.SourcePostsScanned >= DefaultMaxScanned {
				break
			}
			accountReport.SourcePostsScanned++
			eligible, _ := EligibleSourcePost(post, user.ID)
			if !eligible {
				continue
			}
			selected = append(selected, BuildSnapshotPost(resolvedAccount, post))
			if len(selected) >= account.MaxPosts {
				break
			}
		}
		if len(selected) >= account.MaxPosts || strings.TrimSpace(page.NextToken) == "" {
			break
		}
		if page.NextToken == nextToken {
			return FetchAccountData{}, fmt.Errorf("source pagination token did not advance for %q", account.Key)
		}
		nextToken = page.NextToken
	}
	accountReport.EligibleSelected = len(selected)
	return FetchAccountData{Account: snapshotAccount, Posts: selected, Report: accountReport}, nil
}

func validateSourceUser(account SourceAccount, user XUser) (SnapshotAccount, error) {
	sourceUserID := strings.TrimSpace(user.ID)
	if sourceUserID == "" || !isValidSourceUserID(sourceUserID) {
		return SnapshotAccount{}, fmt.Errorf("source account %q has invalid source user ID", account.Key)
	}
	if user.Protected == nil || *user.Protected {
		return SnapshotAccount{}, fmt.Errorf("source account %q is protected or missing protected status", account.Key)
	}
	name := strings.TrimSpace(user.Name)
	handle := strings.TrimSpace(user.Username)
	if name == "" || handle == "" {
		return SnapshotAccount{}, fmt.Errorf("source account %q has incomplete profile", account.Key)
	}
	if !strings.EqualFold(handle, account.Handle) {
		return SnapshotAccount{}, fmt.Errorf("source account %q resolved to unexpected handle %q", account.Key, user.Username)
	}
	return SnapshotAccount{
		RegistryKey:     account.Key,
		SourceUserID:    sourceUserID,
		Handle:          handle,
		Name:            name,
		Description:     user.Description,
		ProfileImageURL: strings.TrimSpace(user.ProfileImageURL),
		Category:        account.Category,
	}, nil
}

func registryHandles(accounts []SourceAccount) []string {
	handles := make([]string, 0, len(accounts))
	for _, account := range accounts {
		handles = append(handles, account.Handle)
	}
	return handles
}
