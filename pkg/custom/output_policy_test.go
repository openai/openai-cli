package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestOutputPolicyPreservesDataAndFailures(t *testing.T) {
	for _, flags := range [][]string{{}, {"--quiet"}, {"--quiet", "--verbose"}, {"--quiet=false"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			var output bytes.Buffer
			cause := cli.Exit("synthetic action failure", 27)
			var quiet bool
			root := &cli.Command{Name: "openai", Writer: &output, ExitErrHandler: func(context.Context, *cli.Command, error) {},
				Flags: []cli.Flag{&cli.StringFlag{Name: "format"}, &cli.StringFlag{Name: "format-error"}},
				Commands: []*cli.Command{{Name: "synthetic", Action: func(ctx context.Context, command *cli.Command) error {
					quiet = quietOutput(ctx)
					io.WriteString(command.Root().Writer, "selected\x00\xffdata\n")
					return cause
				}}},
			}
			configureOutputPolicy(root)
			args := append([]string{"openai"}, flags...)
			err := root.Run(context.Background(), append(args, "synthetic"))
			if !errors.Is(err, cause) || output.String() != "selected\x00\xffdata\n" {
				t.Fatalf("selected bytes or failure changed: %q, %v", output.String(), err)
			}
			wantQuiet := len(flags) > 0 && flags[0] == "--quiet"
			if quiet != wantQuiet {
				t.Fatalf("quiet=%v, want %v", quiet, wantQuiet)
			}
			if quietOutput(context.Background()) {
				t.Fatal("output policy escaped its command context")
			}
		})
	}
}

func TestOutputPolicyProtectsMachineErrors(t *testing.T) {
	for _, tc := range []struct {
		flags []string
		allow bool
	}{
		{nil, true},
		{[]string{"--format", "json"}, false},
		{[]string{"--format", "jsonl"}, false},
		{[]string{"--format", "raw"}, false},
		{[]string{"--format", "yaml"}, false},
		{[]string{"--format", "json", "--format-error", "text"}, true},
		{[]string{"--format-error", "json"}, false},
		{[]string{"--transform-error", "message"}, false},
		{[]string{"--quiet"}, false},
	} {
		t.Run(strings.Join(tc.flags, " "), func(t *testing.T) {
			root := &cli.Command{Name: "openai", Flags: []cli.Flag{
				&cli.StringFlag{Name: "format"}, &cli.StringFlag{Name: "format-error"}, &cli.StringFlag{Name: "transform-error"},
			}, Commands: []*cli.Command{{Name: "synthetic", Action: func(ctx context.Context, _ *cli.Command) error {
				if got := outputDiagnosticsAllowed(ctx); got != tc.allow {
					t.Errorf("diagnostics allowed=%v, want %v", got, tc.allow)
				}
				return nil
			}}}}
			configureOutputPolicy(root)
			args := append([]string{"openai"}, tc.flags...)
			if err := root.Run(context.Background(), append(args, "synthetic")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVerboseResultNeverIncludesFailureDetails(t *testing.T) {
	var output bytes.Buffer
	failure := errors.New("synthetic-secret https://example.invalid/?token=synthetic prompt=private")
	if err := writeVerboseResult(&output, "models list", "json", failure); err != nil {
		t.Fatal(err)
	}
	if output.String() != "Command: models list\nFormat option: json\nCommand result: failed\n" {
		t.Fatalf("unexpected verbose output: %q", output.String())
	}
	cause := errors.New("synthetic sink failure")
	if err := writeVerboseResult(outputPolicyFailureWriter{cause}, "models list", "json", nil); !errors.Is(err, cause) {
		t.Fatalf("diagnostic write failure disappeared: %v", err)
	}
}

func TestOutputPolicyReachesLocalReceiptActions(t *testing.T) {
	for _, args := range [][]string{
		{"--quiet", "@completion", "install", "picker"},
		{"@completion", "--quiet", "install", "picker"},
		{"@completion", "install", "picker", "--quiet"},
		{"--quiet", "@manpages"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := 0
			action := func(ctx context.Context, _ *cli.Command) error {
				calls++
				if !quietOutput(ctx) || outputDiagnosticsAllowed(ctx) {
					t.Error("local receipt action lost quiet policy")
				}
				return nil
			}
			root := &cli.Command{Name: "openai", Commands: []*cli.Command{
				{Name: "@manpages", Action: action},
				{Name: "@completion", Commands: []*cli.Command{{Name: "install", Commands: []*cli.Command{{Name: "picker", Action: action}}}}},
			}}
			configureOutputPolicy(root)
			if err := root.Run(context.Background(), append([]string{"openai"}, args...)); err != nil || calls != 1 {
				t.Fatalf("local dispatch changed: calls=%d, error=%v", calls, err)
			}
		})
	}
}

type outputPolicyFailureWriter struct{ err error }

func (w outputPolicyFailureWriter) Write([]byte) (int, error) { return 0, w.err }
