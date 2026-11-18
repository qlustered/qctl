package apierror

import (
	"reflect"
	"testing"
)

// Loc keeps each element of an object-shaped detail's loc as sent.
func TestObjectShapedDetailKeepsItsLoc(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []interface{}
	}{
		{"string index", `{"detail":{"msg":"Invalid Cron Value","loc":["body","data_sources","1","schedule"]}}`,
			[]interface{}{"body", "data_sources", "1", "schedule"}},
		{"numeric index", `{"detail":{"msg":"Invalid name.","loc":["body","data_sources",1,"name"]}}`,
			[]interface{}{"body", "data_sources", int64(1), "name"}},
		{"absent", `{"detail":{"msg":"Could not connect."}}`, nil},
		{"null", `{"detail":{"msg":"Could not connect.","loc":null}}`, nil},
		{"not a list", `{"detail":{"msg":"Could not connect.","loc":"body"}}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opErr, err := ParseErrorResponse([]byte(tt.body), 400)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(opErr.Loc, tt.want) {
				t.Errorf("Loc = %#v, want %#v", opErr.Loc, tt.want)
			}
		})
	}
}

// A list-shaped detail, FastAPI's validation errors, renders each entry's loc
// into the message and sets no Loc.
func TestListShapedDetailSetsNoLoc(t *testing.T) {
	body := `{"detail":[{"loc":["body","data_sources",0,"s3_secret_key"],"msg":"Input should be a valid string","type":"string_type"}]}`
	opErr, err := ParseErrorResponse([]byte(body), 422)
	if err != nil {
		t.Fatal(err)
	}
	if opErr.Loc != nil {
		t.Errorf("Loc = %#v, want nil", opErr.Loc)
	}
}
