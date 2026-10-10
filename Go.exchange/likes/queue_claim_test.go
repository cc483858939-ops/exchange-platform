package likes

import "testing"

func TestParseSnapshotClaimReplyRejectsMalformedData(t *testing.T) {
	for name, value := range map[string]interface{}{
		"odd fields":       []interface{}{"1"},
		"zero post":        []interface{}{"0", "claim"},
		"invalid post":     []interface{}{"not-a-post", "claim"},
		"negative post":    []interface{}{"-1", "claim"},
		"empty claim":      []interface{}{"1", ""},
		"wrong claim type": []interface{}{"1", int64(2)},
		"over batch":       []interface{}{"1", "a", "2", "b"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSnapshotClaimReply(value, 1); err == nil {
				t.Fatalf("malformed response accepted: %#v", value)
			}
		})
	}
	if _, err := parseSnapshotClaimReply([]interface{}{"1", "a", "1", "b"}, 2); err == nil {
		t.Fatal("duplicate Post ID accepted")
	}
	claims, err := parseSnapshotClaimReply([]interface{}{[]byte("12"), []byte("owner:12")}, 1)
	if err != nil || len(claims) != 1 || claims[0] != (SnapshotClaim{PostID: 12, ClaimID: "owner:12"}) {
		t.Fatalf("claims=%v err=%v", claims, err)
	}
}

func TestParseBehaviorClaimReplyRejectsMalformedData(t *testing.T) {
	for name, value := range map[string]interface{}{
		"odd fields":       []interface{}{"1:2"},
		"empty pair":       []interface{}{"", "claim"},
		"empty claim":      []interface{}{"1:2", ""},
		"wrong claim type": []interface{}{"1:2", int64(2)},
		"over batch":       []interface{}{"1:2", "a", "2:3", "b"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseBehaviorClaimReply(value, 1); err == nil {
				t.Fatalf("malformed response accepted: %#v", value)
			}
		})
	}
	if _, err := parseBehaviorClaimReply([]interface{}{"1:2", "a", "1:2", "b"}, 2); err == nil {
		t.Fatal("duplicate Behavior Pair accepted")
	}
	claims, err := parseBehaviorClaimReply([]interface{}{[]byte("1:2"), []byte("owner:1:2")}, 1)
	if err != nil || len(claims) != 1 || claims[0] != (BehaviorClaim{Pair: "1:2", ClaimID: "owner:1:2"}) {
		t.Fatalf("claims=%v err=%v", claims, err)
	}
	claims, err = parseBehaviorClaimReply([]interface{}{"malformed-pair", "owner:bad"}, 1)
	if err != nil || len(claims) != 1 || claims[0].Pair != "malformed-pair" {
		t.Fatalf("bad Pair must reach per-item isolation: claims=%v err=%v", claims, err)
	}
}
