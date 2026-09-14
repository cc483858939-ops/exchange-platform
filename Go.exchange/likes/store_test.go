package likes

import (
	"reflect"
	"testing"
)

func TestParseScannedUserIDs(t *testing.T) {
	tests := []struct {
		name    string
		values  []string
		want    []uint
		wantErr bool
	}{
		{
			name:   "unique members",
			values: []string{"1", "2", "3"},
			want:   []uint{1, 2, 3},
		},
		{
			name:   "duplicate member in one batch",
			values: []string{"1", "2", "2", "3"},
			want:   []uint{1, 2, 3},
		},
		{
			name:    "invalid member",
			values:  []string{"1", "not-a-user-id"},
			wantErr: true,
		},
		{
			name:    "zero member",
			values:  []string{"0"},
			wantErr: true,
		},
		{
			name:    "overflow member",
			values:  []string{"18446744073709551616"},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseScannedUserIDs(test.values)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseScannedUserIDs(%v) error=nil", test.values)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseScannedUserIDs(%v) error=%v", test.values, err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseScannedUserIDs(%v)=%v want=%v", test.values, got, test.want)
			}
		})
	}
}
