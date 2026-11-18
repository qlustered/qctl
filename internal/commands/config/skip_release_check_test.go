package config

import "testing"

func TestSetContextSkipReleaseCheckSetsAndClearsTheField(t *testing.T) {
	setupAcme(t, "")
	steps := []struct {
		args []string
		want bool
	}{
		{[]string{"--skip-release-check"}, true},
		{[]string{"--output", "json"}, true}, // an unrelated update keeps it
		{[]string{"--skip-release-check=false"}, false},
		{[]string{"--skip-release-check=true"}, true},
	}
	for _, s := range steps {
		args := append([]string{"config", "set-context", "dev"}, s.args...)
		if _, _, err := runContextCommand(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if got := loadContext(t, "dev").SkipReleaseCheck; got != s.want {
			t.Fatalf("after %v: skipReleaseCheck = %v, want %v", args, got, s.want)
		}
	}
}
