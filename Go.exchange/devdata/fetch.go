package devdata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type PreflightResult struct {
	RegistryKey   string
	Handle        string
	SourceUserID  string
	Protected     bool
	ProfileStatus string
	Error         string
}

// SnapshotSourceClient is the small source contract needed by the existing
// preflight and snapshot pipeline. Both the official X API client and the
// RSSHub adapter implement it, so validation and desired-state sync remain
// source-agnostic.
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

var ErrPreflightFailed = errors.New("source preflight failed")

func PreflightSources(ctx context.Context, client SnapshotSourceClient, registry SourceRegistry) ([]PreflightResult, error) {
	if err := ValidateRegistry(registry); err != nil {
		return nil, err
	}
	accounts := registry.EnabledAccounts()
	results := make([]PreflightResult, 0, len(accounts))
	handles := make([]string, 0, len(accounts))
	for _, account := range accounts {
		handles = append(handles, account.Handle)
		results = append(results, PreflightResult{RegistryKey: account.Key, Handle: account.Handle, ProfileStatus: "pending"})
	}
	users, lookupErr := client.LookupUsers(ctx, handles)
	failed := lookupErr != nil
	for index, account := range accounts {
		result := &results[index]
		user, exists := users[strings.ToLower(account.Handle)]
		if !exists {
			result.ProfileStatus = "missing"
			failed = true
			if lookupErr != nil {
				result.Error = lookupErr.Error()
			} else {
				result.Error = "source user was not returned"
			}
			continue
		}
		result.SourceUserID = strings.TrimSpace(user.ID)
		if result.SourceUserID == "" || !isValidSourceUserID(result.SourceUserID) {
			result.ProfileStatus = "invalid_source_user_id"
			result.Error = "source user ID is missing or invalid"
			failed = true
			continue
		}
		if user.Protected == nil {
			result.ProfileStatus = "protected_status_unavailable"
			result.Error = "protected status was not returned"
			failed = true
			continue
		}
		result.Protected = *user.Protected
		if *user.Protected {
			result.ProfileStatus = "protected"
			result.Error = "source account is protected"
			failed = true
			continue
		}
		if strings.TrimSpace(user.Name) == "" || strings.TrimSpace(user.Username) == "" {
			result.ProfileStatus = "profile_incomplete"
			result.Error = "name or username was not returned"
			failed = true
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(user.Username), account.Handle) {
			result.ProfileStatus = "handle_mismatch"
			result.Error = fmt.Sprintf("resolved username %q does not match registry handle", user.Username)
			failed = true
			continue
		}
		result.ProfileStatus = "ok"
		if lookupErr != nil {
			result.Error = lookupErr.Error()
		}
	}
	if failed {
		return results, ErrPreflightFailed
	}
	return results, nil
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
	handles := make([]string, 0, len(accounts))
	for _, account := range accounts {
		handles = append(handles, account.Handle)
	}
	users, err := client.LookupUsers(ctx, handles)
	report := FetchReport{APIRequests: sourceRequestCount(client), PerAccount: make([]FetchAccountReport, 0, len(accounts))}
	if err != nil {
		return Snapshot{}, report, fmt.Errorf("lookup source accounts: %w", err)
	}

	snapshot := Snapshot{Version: DefaultSnapshotVersion, FetchedAt: fetchedAt.UTC()}
	snapshot.Accounts = make([]SnapshotAccount, 0, len(accounts))
	snapshot.Posts = make([]SnapshotPost, 0, len(accounts)*DefaultMaxPosts)
	for _, account := range accounts {
		user, exists := users[strings.ToLower(account.Handle)]
		if !exists {
			return Snapshot{}, report, fmt.Errorf("source account %q was not returned by source", account.Key)
		}
		if user.Protected == nil || *user.Protected {
			return Snapshot{}, report, fmt.Errorf("source account %q is protected or missing protected status", account.Key)
		}
		if strings.TrimSpace(user.ID) == "" || !isValidSourceUserID(user.ID) {
			return Snapshot{}, report, fmt.Errorf("source account %q has invalid source user ID", account.Key)
		}
		if strings.TrimSpace(user.Name) == "" || strings.TrimSpace(user.Username) == "" {
			return Snapshot{}, report, fmt.Errorf("source account %q has incomplete profile", account.Key)
		}
		if !strings.EqualFold(user.Username, account.Handle) {
			return Snapshot{}, report, fmt.Errorf("source account %q resolved to unexpected handle %q", account.Key, user.Username)
		}
		resolvedAccount := account
		resolvedAccount.Handle = strings.TrimSpace(user.Username)
		snapshot.Accounts = append(snapshot.Accounts, SnapshotAccount{
			RegistryKey:     account.Key,
			SourceUserID:    strings.TrimSpace(user.ID),
			Handle:          strings.TrimSpace(user.Username),
			Name:            strings.TrimSpace(user.Name),
			Description:     user.Description,
			ProfileImageURL: strings.TrimSpace(user.ProfileImageURL),
			Category:        account.Category,
		})

		accountReport := FetchAccountReport{RegistryKey: account.Key}
		selected := make([]SnapshotPost, 0, account.MaxPosts)
		nextToken := ""
		for accountReport.SourcePostsScanned < DefaultMaxScanned && len(selected) < account.MaxPosts {
			page, pageErr := client.GetUserPosts(ctx, user.ID, nextToken, 100)
			accountReport.APIRequests++
			if _, counted := client.(sourceRequestCounter); counted {
				report.APIRequests = sourceRequestCount(client)
			} else {
				report.APIRequests++
			}
			if pageErr != nil {
				return Snapshot{}, report, fmt.Errorf("fetch source Posts for %q: %w", account.Key, pageErr)
			}
			for _, post := range page.Posts {
				if accountReport.SourcePostsScanned >= DefaultMaxScanned {
					break
				}
				accountReport.SourcePostsScanned++
				report.SourcePostsScanned++
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
				return Snapshot{}, report, fmt.Errorf("source pagination token did not advance for %q", account.Key)
			}
			nextToken = page.NextToken
		}
		accountReport.EligibleSelected = len(selected)
		report.EligibleSelected += len(selected)
		report.PerAccount = append(report.PerAccount, accountReport)
		snapshot.Posts = append(snapshot.Posts, selected...)
	}
	sortSnapshotAccounts(snapshot.Accounts)
	sortSnapshotPosts(snapshot.Posts)
	if err := ValidateSnapshot(snapshot, registry); err != nil {
		return Snapshot{}, report, err
	}
	return snapshot, report, nil
}

func FormatPreflightResults(results []PreflightResult) string {
	var builder strings.Builder
	builder.WriteString("registry_key\thandle\tsource_user_id\tprotected\tprofile_status\terror\n")
	for _, result := range results {
		fmt.Fprintf(&builder, "%s\t%s\t%s\t%t\t%s\t%s\n", result.RegistryKey, result.Handle, result.SourceUserID, result.Protected, result.ProfileStatus, result.Error)
	}
	return builder.String()
}
