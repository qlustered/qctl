package copycmd

import (
	"encoding/json"
	"io"
	"reflect"
	"testing"

	"github.com/qlustered/qctl/internal/cloud_sources"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

func TestBoundCloudSourceSetFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]json.RawMessage
		exit int
	}{
		{name: "omitted"},
		{name: "lone empty flag", args: []string{"--set", ""}, exit: qerrors.ExitBadUsage},
		{name: "empty flag before valid", args: []string{"--set=", "--set=sftp_port=2222"}, exit: qerrors.ExitBadUsage},
		{
			name: "repeatable values preserve commas and equals",
			args: []string{"--set", "pattern=a,b=c", "--set=sftp-port=2222", "--set=s3_prefix="},
			want: map[string]json.RawMessage{
				"pattern": json.RawMessage(`"a,b=c"`), "sftp_port": json.RawMessage(`2222`),
				"s3_prefix": json.RawMessage(`""`),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var values []string
			var got map[string]json.RawMessage
			cmd := &cobra.Command{
				Use: "cloud-source",
				RunE: func(_ *cobra.Command, _ []string) error {
					var err error
					got, err = cloud_sources.ParseCopyFields(values)
					return err
				},
			}
			bindCloudSourceSetFlag(cmd, &values)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if qerrors.GetExitCode(err) != tc.exit {
				t.Fatalf("exit code = %d, want %d: %v", qerrors.GetExitCode(err), tc.exit, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parsed fields = %v, want %v", got, tc.want)
			}
		})
	}
}
