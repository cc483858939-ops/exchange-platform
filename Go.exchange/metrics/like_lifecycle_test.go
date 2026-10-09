package metrics

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestRecordUserLikeRelationsRemovedCountsRows(t *testing.T) {
	var before, after dto.Metric
	if err := userLikeRelationsRemoved.Write(&before); err != nil {
		t.Fatal(err)
	}
	RecordUserLikeRelationsRemoved(3)
	if err := userLikeRelationsRemoved.Write(&after); err != nil {
		t.Fatal(err)
	}
	if got := after.GetCounter().GetValue() - before.GetCounter().GetValue(); got != 3 {
		t.Fatalf("removed counter delta=%v want 3", got)
	}
	RecordUserLikeRelationsRemoved(0)
	if err := userLikeRelationsRemoved.Write(&before); err != nil {
		t.Fatal(err)
	}
	if got := before.GetCounter().GetValue() - after.GetCounter().GetValue(); got != 0 {
		t.Fatalf("zero removals changed counter, before=%v after=%v", before.GetCounter().GetValue(), after.GetCounter().GetValue())
	}
}
