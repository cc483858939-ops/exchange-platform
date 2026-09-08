package profileavatar

import (
	"strings"
	"testing"
)

func TestParseUserAvatarURLAcceptsLegacyAndV1(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, testCase := range []struct {
		value string
		kind  UserAvatarURLKind
		key   string
	}{
		{
			value: "/api/files/profile-avatars/42/550e8400-e29b-41d4-a716-446655440000.webp",
			kind:  UserAvatarURLLegacy,
			key:   "profile-avatars/42/550e8400-e29b-41d4-a716-446655440000.webp",
		},
		{
			value: "/api/files/profile-avatars/users/v1/42/" + hash + ".png",
			kind:  UserAvatarURLV1,
			key:   "profile-avatars/users/v1/42/" + hash + ".png",
		},
	} {
		key, kind, err := ParseUserAvatarURL(testCase.value, 42)
		if err != nil || key != testCase.key || kind != testCase.kind {
			t.Fatalf("ParseUserAvatarURL(%q)=(%q,%q,%v)", testCase.value, key, kind, err)
		}
	}
}

func TestParseUserAvatarURLRejectsUnsafeOrWrongURLs(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, value := range []string{
		"/api/files/profile-avatars/99/550e8400-e29b-41d4-a716-446655440000.jpg",
		"/api/files/profile-avatars/users/v1/42/" + strings.ToUpper(hash) + ".jpg",
		"/api/files/profile-avatars/users/v1/42/" + hash[:63] + ".jpg",
		"/api/files/profile-avatars/users/v1/42/" + hash + ".webp",
		"/api/files/profile-avatars/users/v1/42/" + hash + ".jpg?cache=1",
		"/api/files/profile-avatars/users/v1/42/" + hash + ".jpg#fragment",
		"/api/files/profile-avatars/users/v1/42/nested/" + hash + ".jpg",
		"/api/files/profile-avatars/42/../550e8400-e29b-41d4-a716-446655440000.jpg",
		"https://example.com/avatar.jpg",
		"/api/files/profile-avatars/42/550e8400-e29b-41d4-a716-446655440000.jpg\r\n",
	} {
		if _, _, err := ParseUserAvatarURL(value, 42); err == nil {
			t.Fatalf("unsafe avatar URL accepted: %q", value)
		}
	}
}

func TestBuildUserV1ObjectKey(t *testing.T) {
	hash := strings.Repeat("b", 64)
	key, err := BuildUserV1ObjectKey(42, hash, ".jpg")
	if err != nil || key != "profile-avatars/users/v1/42/"+hash+".jpg" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	for _, testCase := range []struct {
		hash string
		ext  string
	}{
		{hash: strings.ToUpper(hash), ext: ".jpg"},
		{hash: hash[:63], ext: ".jpg"},
		{hash: hash, ext: ".webp"},
	} {
		if _, err := BuildUserV1ObjectKey(42, testCase.hash, testCase.ext); err == nil {
			t.Fatalf("invalid V1 key input accepted: %#v", testCase)
		}
	}
}
