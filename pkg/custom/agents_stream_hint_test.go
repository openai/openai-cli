package custom

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAgentsStreamSummaryHintUsesOutputPolicy(t *testing.T) {
	for _, test := range []struct {
		flags []string
		want  bool
	}{
		{nil, true},
		{[]string{"--quiet"}, false},
		{[]string{"--quiet=false"}, true},
		{[]string{"--format-error", "json"}, false},
		{[]string{"--format-error", "jsonl"}, false},
		{[]string{"--transform-error", "error.message"}, false},
		{[]string{"--format", "json", "--format-error", "text"}, false},
		{[]string{"--format", "jsonl"}, false},
		{[]string{"--format", "raw"}, false},
		{[]string{"--transform", "delta"}, false},
		{[]string{"--raw-output"}, false},
	} {
		t.Run(strings.Join(test.flags, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			stdout.WriteString("selected result\n")
			root := &cli.Command{Name: "openai", Flags: []cli.Flag{
				&cli.StringFlag{Name: "format"}, &cli.StringFlag{Name: "format-error"},
				&cli.StringFlag{Name: "transform"}, &cli.StringFlag{Name: "transform-error"},
				&cli.BoolFlag{Name: "raw-output"},
			}, Commands: []*cli.Command{{Name: "synthetic", Action: func(ctx context.Context, command *cli.Command) error {
				return writeAgentsStreamSummaryHint(ShowJSONOpts{
					Context: ctx, Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent,
					Format: command.Root().String("format"), Transform: command.Root().String("transform"),
					RawOutput: command.Root().Bool("raw-output"), Stdout: &stdout, Stderr: &stderr,
				}, true)
			}}}}
			configureOutputPolicy(root)
			args := append([]string{"openai"}, test.flags...)
			require.NoError(t, root.Run(context.Background(), append(args, "synthetic")))
			require.Equal(t, "selected result\n", stdout.String())
			if test.want {
				require.Equal(t, "Some fields omitted. Use --format jsonl for complete events.\n", stderr.String())
			} else {
				require.Empty(t, stderr.String())
			}
		})
	}
}

func TestAgentsStreamSummaryHintPreservesDiagnosticFailure(t *testing.T) {
	cause := errors.New("synthetic diagnostic write failure")
	opts := ShowJSONOpts{Context: context.Background(), Format: "text",
		Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent,
		Stderr: outputPolicyFailureWriter{cause},
	}
	err := writeAgentsStreamSummaryHint(opts, true)
	require.ErrorIs(t, err, cause)
	var diagnostic *diagnosticWriteError
	require.ErrorAs(t, err, &diagnostic)
	require.NoError(t, writeAgentsStreamSummaryHint(opts, false))
	opts.Operation = "(resource) responses > (method) create"
	require.NoError(t, writeAgentsStreamSummaryHint(opts, true))
}

func TestAgentsStreamRuntimeWritesOnePolicyControlledHint(t *testing.T) {
	const delta = `{"type":"agent.output.command_execution_output.delta","event_id":"ev_test","session_id":"sess_test","turn_id":"turn_test","item_id":"item_test","output_index":0,"delta":"tool transcript"}`
	const completed = `{"type":"agent.session.turn.completed","session_id":"sess_test","turn_id":"turn_test","turn":{"id":"turn_test","status":"completed","subagent_id":null}}`
	for _, diagnostics := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		ctx := context.WithValue(context.Background(), outputPolicyKey{}, outputPolicy{diagnostics: diagnostics})
		err := ShowJSONIterator(streamItems(delta, delta, completed), -1, ShowJSONOpts{
			Context: ctx, Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent,
			Format: "text", Stdout: &stdout, Stderr: &stderr,
		})
		require.NoError(t, err)
		require.NotContains(t, stdout.String(), "--format")
		require.NotContains(t, stdout.String(), "tool transcript")
		require.Contains(t, stdout.String(), "completed")
		if diagnostics {
			require.Equal(t, 1, strings.Count(stderr.String(), "Some fields omitted."))
		} else {
			require.Empty(t, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	require.NoError(t, ShowJSONIterator(streamItems(delta), 1, ShowJSONOpts{
		Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent,
		Format: "text", Stdout: &stdout, Stderr: &stderr,
	}))
	require.Empty(t, stdout.String(), "omitted tool data must not become No results")
	require.Contains(t, stderr.String(), "Some fields omitted.")
}
