package config

import (
	"strings"
	"testing"
	"time"
)

func TestWatchTimeoutFromEnv(t *testing.T) {
	for _, tt := range []struct {
		name    string
		env     string
		want    time.Duration
		wantSet bool
		wantErr string
	}{
		{name: "unset or empty", env: ""},
		{name: "duration", env: "90s", want: 90 * time.Second, wantSet: true},
		{name: "compound duration", env: "1h30m", want: 90 * time.Minute, wantSet: true},
		{name: "zero is no limit, and set", env: "0", want: 0, wantSet: true},
		{name: "bare number has no unit", env: "60", wantSet: true, wantErr: "invalid QCTL_WATCH_TIMEOUT"},
		{name: "garbage", env: "soon", wantSet: true, wantErr: "invalid QCTL_WATCH_TIMEOUT"},
		{name: "negative", env: "-5s", wantSet: true, wantErr: "must not be negative"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("QCTL_WATCH_TIMEOUT", tt.env)
			got, set, err := WatchTimeoutFromEnv()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tt.want || set != tt.wantSet {
				t.Errorf("WatchTimeoutFromEnv() = (%s, %v), want (%s, %v)", got, set, tt.want, tt.wantSet)
			}
		})
	}
}
