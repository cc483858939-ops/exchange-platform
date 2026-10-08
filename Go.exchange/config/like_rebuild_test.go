package config

import (
	"testing"
	"time"
)

func TestLikeDeletionTokenConfiguration(t *testing.T) {
	t.Setenv("LIKE_DELETION_TOMBSTONE_EXPIRY_ENABLED", "")
	t.Setenv("LIKE_DELETION_TOMBSTONE_TTL", "")
	t.Setenv("LIKE_REBUILD_TOKEN_TTL", "")
	if !LikeDeletionTombstoneExpiryEnabled() || LikeDeletionTombstoneTTL() != 24*time.Hour || LikeRebuildTokenTTL() != 30*time.Second {
		t.Fatal("unexpected deletion expiry defaults")
	}
	t.Setenv("LIKE_DELETION_TOMBSTONE_EXPIRY_ENABLED", "false")
	if LikeDeletionTombstoneExpiryEnabled() {
		t.Fatal("explicit disable ignored")
	}
	t.Setenv("LIKE_DELETION_TOMBSTONE_EXPIRY_ENABLED", "true")
	t.Setenv("LIKE_DELETION_TOMBSTONE_TTL", "2h")
	t.Setenv("LIKE_REBUILD_TOKEN_TTL", "10s")
	if !LikeDeletionTombstoneExpiryEnabled() || LikeDeletionTombstoneTTL() != 2*time.Hour || LikeRebuildTokenTTL() != 10*time.Second {
		t.Fatal("configuration ignored")
	}
	for _, value := range []string{"0s", "-1s", "invalid", "1ns"} {
		t.Setenv("LIKE_DELETION_TOMBSTONE_TTL", value)
		t.Setenv("LIKE_REBUILD_TOKEN_TTL", value)
		if LikeDeletionTombstoneTTL() < time.Millisecond || LikeRebuildTokenTTL() < time.Millisecond {
			t.Fatal("sub-millisecond Redis TTL")
		}
	}
}
