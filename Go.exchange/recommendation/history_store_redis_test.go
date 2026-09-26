package recommendation

import (
	"strings"
	"testing"
)

func TestUserHistoryKeyIsStableAndScopedToUser(t *testing.T) {
	key := userHistoryKey(42)
	if want := "recommendation:user:served:v1:42"; key != want {
		t.Fatalf("key=%q want %q", key, want)
	}
	if userHistoryKey(42) != key {
		t.Fatal("same user ID did not produce a stable key")
	}
	if userHistoryKey(43) == key {
		t.Fatal("different user IDs produced the same key")
	}
}

func TestGuestHistoryKeyHashesSessionID(t *testing.T) {
	sessionID := "4ca3706b-197e-4f63-8f51-f99176f8b61c"
	key := guestHistoryKey(sessionID)
	if !strings.HasPrefix(key, "recommendation:guest:served:v1:") {
		t.Fatalf("key=%q does not use the v1 namespace", key)
	}
	if len(key) != len("recommendation:guest:served:v1:")+64 {
		t.Fatalf("key length=%d want %d", len(key), len("recommendation:guest:served:v1:")+64)
	}
	if strings.Contains(key, sessionID) {
		t.Fatalf("raw session ID leaked into Redis key: %q", key)
	}
	if guestHistoryKey(sessionID) != key {
		t.Fatal("same session ID did not produce a stable key")
	}
	if guestHistoryKey("4ca3706b-197e-4f63-8f51-f99176f8b62c") == key {
		t.Fatal("different session IDs produced the same key")
	}
}

func TestHistoryMembersUseDecimalIDsAndDeduplicate(t *testing.T) {
	members := historyMembers([]uint{0, 10, 10, 3, 0, 7}, 123)
	if len(members) != 3 {
		t.Fatalf("members=%d want 3", len(members))
	}
	for index, want := range []string{"10", "3", "7"} {
		if members[index].Member != want || members[index].Score != 123 {
			t.Fatalf("member[%d]=%#v want %s@123", index, members[index], want)
		}
	}
}
