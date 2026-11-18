package releases

import (
	"reflect"
	"testing"
)

func TestContextsRequiringMatchesNormalizedServers(t *testing.T) {
	ix := &Index{Servers: map[string]*Observation{
		"https://prod.example.test:443":  {QctlVersion: "2.135.1"},
		"http://localhost:8000":          {AtlasVersion: "dev"}, // no release selected
		"https://other.example.test:443": {QctlVersion: "2.134.0"},
	}}
	got := ix.ContextsRequiring(map[string]string{
		"prod":    "https://PROD.example.test/",
		"prod-eu": "https://prod.example.test:443",
		"local":   "http://localhost:8000",
		"broken":  "://nope",
		"empty":   "",
		"unseen":  "https://new.example.test",
	})
	want := map[string][]string{"2.135.1": {"prod", "prod-eu"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ContextsRequiring = %v, want %v", got, want)
	}
}
