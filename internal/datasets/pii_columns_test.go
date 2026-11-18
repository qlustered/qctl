package datasets

import (
	"reflect"
	"testing"
)

func TestDroppedPiiColumns(t *testing.T) {
	tests := []struct {
		name   string
		stored []string
		sent   *[]string
		want   []string
	}{
		{"key not sent", []string{"a"}, nil, nil},
		{"unchanged", []string{"a", "b"}, &[]string{"b", "a"}, nil},
		{"added only", []string{"a"}, &[]string{"a", "c"}, nil},
		{"cleared", []string{"a", "b"}, &[]string{}, []string{"a", "b"}},
		{"stored order, each once", []string{"c", "a", "c", "b"}, &[]string{"b"}, []string{"c", "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := droppedPiiColumns(tt.stored, tt.sent); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("droppedPiiColumns = %v, want %v", got, tt.want)
			}
		})
	}
}
