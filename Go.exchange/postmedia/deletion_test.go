package postmedia

import (
	"strings"
	"testing"
)

func TestUserDeletionKeysConfinesRemovalToTheOwnedUpload(t *testing.T) {
	id := "550e8400-e29b-41d4-a716-446655440000"
	paths, err := BuildUserV1ObjectPaths(42, id, ".webp", ".png")
	if err != nil {
		t.Fatal(err)
	}
	mediaID, keys, err := UserDeletionKeys(42, PublicURL(paths.MediumObjectKey), PublicURL(paths.LargeObjectKey))
	if err != nil || mediaID != id || len(keys) != 6 {
		t.Fatalf("id=%s keys=%v err=%v", mediaID, keys, err)
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, UserV1ObjectPrefix+"42/"+id+"/") {
			t.Fatalf("escaped upload folder: %s", key)
		}
	}
	for _, test := range []struct {
		owner         uint
		medium, large string
	}{
		{43, PublicURL(paths.MediumObjectKey), PublicURL(paths.LargeObjectKey)},
		{42, PublicURL(paths.LargeObjectKey), PublicURL(paths.MediumObjectKey)},
		{42, PublicURL(paths.MediumObjectKey), strings.Replace(PublicURL(paths.LargeObjectKey), id, "550e8400-e29b-41d4-a716-446655440001", 1)},
		{42, "https://remote.invalid" + PublicURL(paths.MediumObjectKey), PublicURL(paths.LargeObjectKey)},
		{42, PublicURL(paths.OriginalObjectKey), PublicURL(paths.LargeObjectKey)},
		{42, PublicURL(paths.MediumObjectKey) + "?download=1", PublicURL(paths.LargeObjectKey)},
	} {
		if _, _, err := UserDeletionKeys(test.owner, test.medium, test.large); err == nil {
			t.Fatalf("unsafe deletion accepted: %+v", test)
		}
	}
}
