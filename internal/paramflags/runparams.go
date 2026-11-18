package paramflags

import (
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

const (
	paramsFileFlag = "params-file"
	periodFlag     = "period"
)

// RegisterRunParamFlags registers --params-file and --period.
func RegisterRunParamFlags(cmd *cobra.Command) {
	cmd.Flags().String(paramsFileFlag, "", "YAML mapping of run parameters; - reads stdin")
	cmd.Flags().String(periodFlag, "", "String value of the deliverable's period parameter")
}

// RunParamInput reads --params-file and --period back. A blank value, both
// flags together, and a parameters file deliverables.ReadRunParams refuses
// are bad-usage errors.
func RunParamInput(cmd *cobra.Command) (deliverables.RunParamInput, error) {
	var in deliverables.RunParamInput
	if err := cmdutil.RejectBlankFlags(cmd, paramsFileFlag, periodFlag); err != nil {
		return in, err
	}
	if cmd.Flags().Changed(paramsFileFlag) && cmd.Flags().Changed(periodFlag) {
		return in, qerrors.NewBadUsage("--params-file and --period are mutually exclusive")
	}
	if cmd.Flags().Changed(periodFlag) {
		period, _ := cmd.Flags().GetString(periodFlag)
		in.Period = &period
	}
	if !cmd.Flags().Changed(paramsFileFlag) {
		return in, nil
	}
	path, _ := cmd.Flags().GetString(paramsFileFlag)
	params, err := deliverables.ReadRunParams(path, cmd.InOrStdin())
	if err != nil {
		return in, err
	}
	in.Params = params
	return in, nil
}
