package cmdutil

import (
	"strings"
	"testing"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

func TestWaitFlags(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantWait    bool
		wantTimeout time.Duration
		wantErr     string
	}{
		{name: "neither flag", args: nil},
		{name: "--wait takes the default timeout", args: []string{"--wait"}, wantWait: true, wantTimeout: 900 * time.Second},
		{name: "--wait-timeout is seconds", args: []string{"--wait", "--wait-timeout", "60"}, wantWait: true, wantTimeout: time.Minute},
		{name: "--wait-timeout alone is refused", args: []string{"--wait-timeout", "60"}, wantErr: "--wait-timeout requires --wait"},
		{name: "--wait=false with a timeout is refused", args: []string{"--wait=false", "--wait-timeout", "60"}, wantErr: "--wait-timeout requires --wait"},
		{name: "a zero timeout is refused", args: []string{"--wait", "--wait-timeout", "0"}, wantErr: "--wait-timeout must be at least 1 second, got 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			AddWaitFlags(cmd, "it")
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}

			timeout, wait, err := WaitFlags(cmd)
			if tt.wantErr != "" {
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if wait != tt.wantWait || timeout != tt.wantTimeout {
				t.Errorf("WaitFlags = (%s, %t), want (%s, %t)", timeout, wait, tt.wantTimeout, tt.wantWait)
			}
		})
	}
}
