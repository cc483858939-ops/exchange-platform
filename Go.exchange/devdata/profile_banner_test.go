package devdata

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
)

func TestRSSHubProfileBannerNamespacePresence(t *testing.T) {
	tests := []struct {
		name          string
		rss           string
		wantPresent   bool
		wantBannerURL string
	}{
		{
			name:          "namespaced URL",
			rss:           `<rss xmlns:nexusfeed="urn:nexusfeed:rss:1.0"><channel><title>Twitter @Fixture</title><nexusfeed:profileBanner>https://pbs.twimg.com/profile_banners/7/123</nexusfeed:profileBanner></channel></rss>`,
			wantPresent:   true,
			wantBannerURL: "https://pbs.twimg.com/profile_banners/7/123",
		},
		{
			name:          "present empty",
			rss:           `<rss xmlns:nexusfeed="urn:nexusfeed:rss:1.0"><channel><title>Twitter @Fixture</title><nexusfeed:profileBanner></nexusfeed:profileBanner></channel></rss>`,
			wantPresent:   true,
			wantBannerURL: "",
		},
		{
			name:          "missing",
			rss:           `<rss xmlns:nexusfeed="urn:nexusfeed:rss:1.0"><channel><title>Twitter @Fixture</title></channel></rss>`,
			wantPresent:   false,
			wantBannerURL: "",
		},
		{
			name:          "alternate namespace prefix",
			rss:           `<rss xmlns:nf="urn:nexusfeed:rss:1.0"><channel><title>Twitter @Fixture</title><nf:profileBanner> https://pbs.twimg.com/profile_banners/7/456 </nf:profileBanner></channel></rss>`,
			wantPresent:   true,
			wantBannerURL: "https://pbs.twimg.com/profile_banners/7/456",
		},
		{
			name:          "wrong namespace",
			rss:           `<rss xmlns:other="urn:other"><channel><title>Twitter @Fixture</title><other:profileBanner>https://example.com/banner.jpg</other:profileBanner></channel></rss>`,
			wantPresent:   false,
			wantBannerURL: "",
		},
		{
			name: "whitespace",
			rss: `<rss xmlns:nexusfeed="urn:nexusfeed:rss:1.0"><channel><title>Twitter @Fixture</title><nexusfeed:profileBanner>
    https://pbs.twimg.com/profile_banners/7/789
</nexusfeed:profileBanner></channel></rss>`,
			wantPresent:   true,
			wantBannerURL: "https://pbs.twimg.com/profile_banners/7/789",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var document rssHubDocument
			if err := xml.Unmarshal([]byte(test.rss), &document); err != nil {
				t.Fatal(err)
			}
			feed, err := parseRSSHubFeed("Fixture", document)
			if err != nil {
				t.Fatal(err)
			}
			if feed.user.ProfileBannerPresent != test.wantPresent || feed.user.ProfileBannerURL != test.wantBannerURL {
				t.Fatalf("profile banner present=%t URL=%q, want present=%t URL=%q", feed.user.ProfileBannerPresent, feed.user.ProfileBannerURL, test.wantPresent, test.wantBannerURL)
			}
		})
	}
}

func TestValidateSourceUserCopiesProfileBannerStates(t *testing.T) {
	account := SourceAccount{Key: "source", Handle: "source", Category: "test"}
	falseValue := false
	tests := []struct {
		name        string
		present     bool
		url         string
		wantPresent bool
		wantURL     string
	}{
		{name: "absent", present: false, wantPresent: false},
		{name: "present empty", present: true, wantPresent: true},
		{name: "present URL", present: true, url: "  https://pbs.twimg.com/profile_banners/7/123  ", wantPresent: true, wantURL: "https://pbs.twimg.com/profile_banners/7/123"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := validateSourceUser(account, XUser{
				ID:                   "rsshub:source",
				Name:                 "Source",
				Username:             "source",
				ProfileBannerPresent: test.present,
				ProfileBannerURL:     test.url,
				Protected:            &falseValue,
			})
			if err != nil {
				t.Fatal(err)
			}
			if resolved.ProfileBannerPresent != test.wantPresent || resolved.ProfileBannerURL != test.wantURL {
				t.Fatalf("snapshot profile banner present=%t URL=%q, want present=%t URL=%q", resolved.ProfileBannerPresent, resolved.ProfileBannerURL, test.wantPresent, test.wantURL)
			}
		})
	}
}

func TestSnapshotAccountJSONPreservesExplicitEmptyProfileBanner(t *testing.T) {
	account := SnapshotAccount{
		RegistryKey:          "source",
		SourceUserID:         "rsshub:source",
		Handle:               "source",
		Name:                 "Source",
		ProfileBannerPresent: true,
		ProfileBannerURL:     "",
		Category:             "test",
	}
	payload, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if !strings.Contains(text, `"profile_banner_present":true`) || !strings.Contains(text, `"profile_banner_url":""`) {
		t.Fatalf("serialized account=%s", text)
	}
	var decoded SnapshotAccount
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.ProfileBannerPresent || decoded.ProfileBannerURL != "" {
		t.Fatalf("decoded account=%#v", decoded)
	}
}

func TestValidateSnapshotRejectsProfileBannerURLWithoutPresence(t *testing.T) {
	snapshot := testSnapshot(strings.Repeat("m", MinTextRunes))
	snapshot.Accounts[0].ProfileBannerURL = "https://pbs.twimg.com/profile_banners/7/123"
	if err := ValidateSnapshot(snapshot, testRegistry()); err == nil {
		t.Fatal("snapshot with banner URL but absent metadata was accepted")
	}
}
