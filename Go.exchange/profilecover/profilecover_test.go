package profilecover

import (
	"strings"
	"testing"
)

func TestBuildAndParseUserV1CoverKey(t *testing.T) {
	hash := strings.Repeat("a", 64)
	key, err := BuildUserV1ObjectKey(42, hash, ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	wantKey := UserV1ObjectPrefix + "42/" + hash + ".jpg"
	if key != wantKey || !IsPublicObjectKey(key) {
		t.Fatalf("key=%q want=%q public=%v", key, wantKey, IsPublicObjectKey(key))
	}
	parsed, err := ParseUserCoverURL(FilesURLPrefix+key, 42)
	if err != nil || parsed != key {
		t.Fatalf("ParseUserCoverURL=%q,%v", parsed, err)
	}
}

func TestCoverURLRejectsUnsafeOrWrongValues(t *testing.T) {
	hash := strings.Repeat("b", 64)
	for _, value := range []string{
		"https://example.com/cover.jpg",
		"//example.com/cover.jpg",
		FilesURLPrefix + UserV1ObjectPrefix + "99/" + hash + ".jpg",
		FilesURLPrefix + UserV1ObjectPrefix + "42/" + strings.ToUpper(hash) + ".jpg",
		FilesURLPrefix + UserV1ObjectPrefix + "42/" + hash[:63] + ".jpg",
		FilesURLPrefix + UserV1ObjectPrefix + "42/" + hash + ".webp",
		FilesURLPrefix + UserV1ObjectPrefix + "42/nested/" + hash + ".jpg",
		FilesURLPrefix + UserV1ObjectPrefix + "42/../" + hash + ".jpg",
		FilesURLPrefix + UserV1ObjectPrefix + "42/" + hash + ".jpg?cache=1",
		FilesURLPrefix + UserV1ObjectPrefix + "42/" + hash + ".jpg#fragment",
		FilesURLPrefix + UserV1ObjectPrefix + "42/" + hash + ".jpg\r\n",
		FilesURLPrefix + UserV1ObjectPrefix + "042/" + hash + ".jpg",
	} {
		if _, err := ParseUserCoverURL(value, 42); err == nil {
			t.Fatalf("unsafe cover URL accepted: %q", value)
		}
	}
	for _, key := range []string{
		"profile-covers/users/v1/42/" + strings.ToUpper(hash) + ".jpg",
		"profile-covers/users/v1/42/" + hash + ".gif",
		"profile-covers/users/v1/42/" + hash + ".jpg/extra",
		"profile-covers/users/v1/420/" + hash + ".jpg\r\n",
		"profile-avatars/users/v1/42/" + hash + ".jpg",
	} {
		if IsPublicObjectKey(key) {
			t.Fatalf("unsafe cover object key accepted: %q", key)
		}
	}
}

func TestBuildUserV1CoverKeyRejectsInvalidInputs(t *testing.T) {
	hash := strings.Repeat("c", 64)
	for _, testCase := range []struct {
		userID uint
		hash   string
		ext    string
	}{
		{userID: 0, hash: hash, ext: ".jpg"},
		{userID: 42, hash: strings.ToUpper(hash), ext: ".jpg"},
		{userID: 42, hash: hash[:63], ext: ".jpg"},
		{userID: 42, hash: hash, ext: ".webp"},
		{userID: 42, hash: hash, ext: ".JPG"},
	} {
		if _, err := BuildUserV1ObjectKey(testCase.userID, testCase.hash, testCase.ext); err == nil {
			t.Fatalf("invalid input accepted: %#v", testCase)
		}
	}
}
