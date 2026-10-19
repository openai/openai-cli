package batches

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	clioutput "github.com/openai/openai-cli/internal/output"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Unit workflows use shared request and JSON output implementations. Full
// assembly policy is exercised by batch_transport_test and cmd/openai contracts.
func testOptions() Options {
	return Options{
		DefaultRequestOptions: func(cmd *cli.Command) []option.RequestOption {
			return requestconfig.DefaultOptions(cmd, "test")
		},
		RequestOptions: func(cmd *cli.Command, stdin io.Reader) ([]option.RequestOption, error) {
			return requestconfig.FlagOptions(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, requestconfig.EmptyBody, false, stdin, requestconfig.PreparationHooks{})
		},
		ShowResult: func(ctx context.Context, cmd *cli.Command, value gjson.Result, operation string, out, stderr io.Writer) error {
			root := cmd.Root()
			data, err := clioutput.FormatJSON(value, clioutput.FormatOptions{Format: root.String("format"), Transform: root.String("transform"), RawOutput: root.Bool("raw-output")}, out, false)
			if err != nil {
				return err
			}
			_, err = (clioutput.Writer{Context: ctx, Sink: out}).Write(data)
			return err
		},
		HumanTerminal:   func(context.Context, *cli.Command) bool { return false },
		APIErrorMessage: func(_ *cli.Command, _ error, _ *openai.Error) string { return "synthetic API error" },
	}
}

func TestMain(m *testing.M) {
	if handled, err := RunBatchOutputHelper(os.Args); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if handled, err := RunBatchDirectoryHelper(os.Args); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
