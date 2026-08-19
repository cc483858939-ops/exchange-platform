package recommendation

import (
	"reflect"
	"testing"
)

func TestNormalizeDirtyUsersDeduplicatesZeroAndSorts(t *testing.T) {
	got := normalizeDirtyUsers([]uint{9, 0, 4, 9, 2, 4, 1})
	if want := []uint{1, 2, 4, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized users=%v want=%v", got, want)
	}
}

func TestNormalizeDirtyReasonCapsAt64Characters(t *testing.T) {
	got := normalizeDirtyReason("12345678901234567890123456789012345678901234567890123456789012345")
	if len(got) != 64 {
		t.Fatalf("reason length=%d want=64", len(got))
	}
}
