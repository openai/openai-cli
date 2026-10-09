package custom

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const agentsTraceFixture = `{"id":"turn_synthetic","object":"agent.session.trace","session_id":"sess_synthetic","created_at":1728000000,"otlp":{"resourceSpans":[{"resource":{"attributes":[{"key":"synthetic.detail","value":{"stringValue":"trace detail"}}]}}]}}`
const agentsUpdatedFixture = `{"id":"agent_synthetic","object":"agent","name":"Analyst","model":"synthetic-model","created_at":1728000000,"updated_at":1728000001,"instructions":"synthetic instructions","metadata":{},"tools":[],"reasoning":{"effort":"medium"},"text":{"format":{"type":"text"},"verbosity":"medium"},"service_tier":"auto","multi_agent":{"enabled":false,"max_concurrent_subagents":null}}`

func TestAgentsResourceRuntimeReportsTraceOmission(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := ShowJSONIterator(streamItems(agentsTraceFixture), -1, ShowJSONOpts{
		Operation: "(resource) beta.agents.sessions.traces > (method) list", OutputKind: OutputPageItem,
		Stdout: &stdout, Stderr: &stderr,
	})
	require.NoError(t, err)
	require.Contains(t, stdout.String(), "turn_synthetic")
	require.NotContains(t, stdout.String(), "trace detail")
	require.Equal(t, resourceSummaryHint+"\n", stderr.String())
}

func TestAgentsResourceRuntimeSummarizesRequiredUpdatedAt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := ShowJSON(gjson.Parse(agentsUpdatedFixture), ShowJSONOpts{
		Operation: "(resource) beta.agents > (method) retrieve", OutputKind: OutputResponse,
		Stdout: &stdout, Stderr: &stderr,
	})
	require.NoError(t, err)
	require.Contains(t, stdout.String(), "agent_synthetic")
	require.Contains(t, stdout.String(), "Analyst")
	require.NotContains(t, stdout.String(), "synthetic instructions")
	require.NotContains(t, stdout.String(), "Updated at")
	require.Equal(t, resourceSummaryHint+"\n", stderr.String())
}

func TestAgentsResourceRuntimePreservesDataModesAndExtraction(t *testing.T) {
	for _, format := range []string{"json", "jsonl", "pretty", "raw", "yaml", "explore"} {
		for _, explicit := range []bool{false, true} {
			t.Run(format+"/explicit="+map[bool]string{false: "false", true: "true"}[explicit], func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				err := ShowJSON(gjson.Parse(agentsUpdatedFixture), ShowJSONOpts{
					Operation: "(resource) beta.agents > (method) retrieve", OutputKind: OutputResponse,
					Format: format, ExplicitFormat: explicit, Stdout: &stdout, Stderr: &stderr,
				})
				require.NoError(t, err)
				require.Contains(t, stdout.String(), "synthetic instructions")
				require.Contains(t, stdout.String(), "updated_at")
				require.NotContains(t, stderr.String(), resourceSummaryHint)
			})
		}
	}
	for _, opts := range []ShowJSONOpts{
		{Format: "text", Transform: "instructions"},
		{Format: "auto", Transform: "instructions", RawOutput: true},
		{Format: "text", RawOutput: true},
	} {
		var stdout, stderr bytes.Buffer
		opts.Operation, opts.OutputKind = "(resource) beta.agents > (method) retrieve", OutputResponse
		opts.Stdout, opts.Stderr = &stdout, &stderr
		require.NoError(t, ShowJSON(gjson.Parse(agentsUpdatedFixture), opts))
		require.Contains(t, stdout.String(), "synthetic instructions")
		require.Empty(t, stderr.String())
	}
}

func TestAgentsResourceRuntimeHintHonorsDiagnosticPolicy(t *testing.T) {
	for _, flags := range [][]string{nil, {"--quiet"}, {"--format-error", "json"}, {"--transform-error", "error.message"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			root := &cli.Command{Name: "openai", Flags: []cli.Flag{
				&cli.StringFlag{Name: "format"}, &cli.StringFlag{Name: "format-error"}, &cli.StringFlag{Name: "transform-error"},
			}, Action: func(ctx context.Context, _ *cli.Command) error {
				return ShowJSON(gjson.Parse(agentsUpdatedFixture), ShowJSONOpts{
					Context: ctx, Operation: "(resource) beta.agents > (method) retrieve", OutputKind: OutputResponse,
					Stdout: &stdout, Stderr: &stderr,
				})
			}}
			configureOutputPolicy(root)
			require.NoError(t, root.Run(context.Background(), append([]string{"openai"}, flags...)))
			require.NotContains(t, stdout.String(), "synthetic instructions")
			if len(flags) == 0 {
				require.Equal(t, resourceSummaryHint+"\n", stderr.String())
			} else {
				require.Empty(t, stderr.String())
			}
		})
	}
}

func TestAgentsResourceRuntimeListHintsOnceAndPreservesFailures(t *testing.T) {
	pageErr, hintErr := errors.New("synthetic page failure"), errors.New("synthetic hint failure")
	for _, failedSink := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		opts := ShowJSONOpts{Operation: "(resource) beta.agents.sessions.traces > (method) list", OutputKind: OutputPageItem,
			Stdout: &stdout, Stderr: &stderr}
		source := &transformTestIterator{items: []any{outputJSON{gjson.Parse(agentsTraceFixture)}, outputJSON{gjson.Parse(agentsTraceFixture)}}, err: pageErr}
		if failedSink {
			opts.Stderr = resourceSummaryTestWriter(func(data []byte) (int, error) {
				require.Equal(t, 3, source.calls)
				require.Equal(t, resourceSummaryHint+"\n", string(data))
				return 0, hintErr
			})
		}
		err := ShowJSONIterator(source, -1, opts)
		require.ErrorIs(t, err, pageErr)
		if failedSink {
			require.ErrorIs(t, err, hintErr)
		} else {
			require.Equal(t, resourceSummaryHint+"\n", stderr.String())
		}
		require.Equal(t, 2, strings.Count(stdout.String(), "turn_synthetic"))
		require.NotContains(t, stdout.String(), "trace detail")
		require.NotContains(t, stdout.String(), resourceSummaryHint)
	}
}

func TestAgentsResourceRuntimeUnknownFieldsKeepFullRecord(t *testing.T) {
	value := strings.TrimSuffix(agentsUpdatedFixture, "}") + `,"future_setting":null}`
	var stdout, stderr bytes.Buffer
	require.NoError(t, ShowJSON(gjson.Parse(value), ShowJSONOpts{
		Operation: "(resource) beta.agents > (method) retrieve", OutputKind: OutputResponse,
		Stdout: &stdout, Stderr: &stderr,
	}))
	require.Contains(t, stdout.String(), "synthetic instructions")
	require.Contains(t, stdout.String(), "Future setting")
	require.Empty(t, stderr.String())
}

func TestAgentsResourceRuntimeCreateAndUpdateRetainSummaryHint(t *testing.T) {
	for _, method := range []string{"create", "update"} {
		var stdout, stderr bytes.Buffer
		require.NoError(t, ShowJSON(gjson.Parse(agentsUpdatedFixture), ShowJSONOpts{
			Operation: "(resource) beta.agents > (method) " + method, OutputKind: OutputResponse,
			Stdout: &stdout, Stderr: &stderr,
		}))
		require.NotContains(t, stdout.String(), "synthetic instructions")
		require.Equal(t, resourceSummaryHint+"\n", stderr.String())
	}
}
