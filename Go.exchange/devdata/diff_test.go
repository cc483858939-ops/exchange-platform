package devdata

import (
	"reflect"
	"testing"
)

func TestCalculateDesiredStateDiff(t *testing.T) {
	got := CalculateDesiredStateDiff(
		[]string{"keep", "retire"},
		[]string{"reactivate"},
		[]string{"keep", "reactivate", "insert"},
	)
	want := DesiredStateDiff{
		Keep:       []string{"keep"},
		Reactivate: []string{"reactivate"},
		Insert:     []string{"insert"},
		Retire:     []string{"retire"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff=%#v want %#v", got, want)
	}
}
