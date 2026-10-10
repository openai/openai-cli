package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestCostReportRegistrationFormatMetadata(t *testing.T) {
	root := &cli.Command{Name: "openai"}
	registerCostReportCommands(root)
	costs := root.Command("costs")
	require.NotNil(t, costs)
	report := costs.Command("report")
	require.NotNil(t, report)
	require.Equal(t, map[string][]string{"format": {"auto", "text", "json"}}, report.Metadata["completion-root-flag-values"])
	require.NotNil(t, report.Metadata["help-content"])
	require.Nil(t, costs.Metadata["completion-root-flag-values"], "the override belongs only to the selected report command")
}

func TestCostReportAuthGuidanceContextFallbacks(t *testing.T) {
	const fallback = "Cost reports require an organization Admin API key.\n" +
		"Replace an explicit --admin-api-key value; otherwise set OPENAI_ADMIN_KEY.\n" +
		"A project API key cannot replace an Admin key."
	for _, tc := range []struct {
		name       string
		contextual bool
		nilCommand bool
		headers    []string
	}{
		{name: "absent command context"},
		{name: "nil command", contextual: true, nilCommand: true},
		{name: "unrelated header", contextual: true, headers: []string{"--header", "X-Cost-Recovery: synthetic-safe-value"}},
		{name: "malformed header", contextual: true, headers: []string{"-H", "Authorization: Bearer synthetic-unused-header",
			"--header", "synthetic-malformed-header"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apierr := &openai.Error{StatusCode: http.StatusUnauthorized}
			var failure error = &costReportRequestError{apierr}
			if tc.contextual {
				var command *cli.Command
				if !tc.nilCommand {
					command = &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
						Flags: []cli.Flag{NewRequestHeaderFlag()}, Action: func(context.Context, *cli.Command) error { return nil }}
					require.NoError(t, command.Run(t.Context(), append([]string{"openai"}, tc.headers...)))
				}
				failure = &commandError{command: command, err: failure}
			}
			require.NotPanics(t, func() {
				require.Equal(t, fallback, costReportAPIErrorMessage(failure, apierr))
			})
			require.ErrorIs(t, failure, apierr, "presentation must preserve the original API error")
		})
	}
}

func TestCostReportWriterFailures(t *testing.T) {
	for _, format := range []string{"text", "json", "csv"} {
		t.Run(format, func(t *testing.T) {
			err := writeCostReport(t.Context(), costReportFailWriter{}, format, projectCostReport{
				Rows: []transformers.ProjectCostRow{{Currency: "usd", Amount: "0.3"}},
			})
			require.ErrorIs(t, err, io.ErrClosedPipe)
		})
	}
}

type costReportFailWriter struct{}

func (costReportFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCostReportWriterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, format := range []string{"text", "json", "csv"} {
		var out bytes.Buffer
		err := writeCostReport(ctx, &out, format, projectCostReport{})
		require.True(t, errors.Is(err, context.Canceled))
		require.Empty(t, out.String())
	}
}

func TestCostReportCSVText(t *testing.T) {
	for _, value := range []string{"=SUM(A1)", "+1", "-2", "@cmd", "  =x", "\tplain", "a\nb", "'quoted"} {
		require.Equal(t, "'"+value, costReportCSVText(value))
	}
	for _, value := range []string{"proj_example", "usd", "", "with,comma", "with\"quote"} {
		require.Equal(t, value, costReportCSVText(value))
	}
}

func TestCostReportInterruptHandoffWaitsForDeliveredSignal(t *testing.T) {
	interrupts := make(chan os.Signal, 1)
	interrupts <- os.Interrupt
	handled := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		stopCostReportInterrupts(interrupts, handled)
		close(stopped)
	}()
	t.Cleanup(func() { <-stopped })
	defer close(handled)
	// Model a delivered notification whose handler has not yet canceled the context.
	require.Equal(t, os.Interrupt, <-interrupts)
	_, open := <-interrupts
	require.False(t, open, "handoff must stop accepting new notifications")
	select {
	case <-stopped:
		t.Fatal("handoff returned before the delivered notification was handled")
	default:
	}
}

// Isolate the real signal so this regression cannot interrupt other tests.
func TestCostReportCancellationPreservesJoinedCauses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal cannot send os.Interrupt on Windows")
	}
	const childSetting = "OPENAI_COST_REPORT_CAUSE_TEST"
	if os.Getenv(childSetting) != "1" {
		binary, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, binary, "-test.run=^TestCostReportCancellationPreservesJoinedCauses$")
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "OPENAI_") {
				child.Env = append(child.Env, entry)
			}
		}
		child.Env = append(child.Env, childSetting+"=1", "OPENAI_ADMIN_KEY=synthetic-cost-admin")
		output, err := child.CombinedOutput()
		require.NoError(t, ctx.Err())
		require.NoError(t, err, "%s", output)
		return
	}
	root := &cli.Command{
		Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags:    []cli.Flag{&cli.StringFlag{Name: "base-url", Value: "http://127.0.0.1:1"}},
		Metadata: map[string]any{mtlsHTTPClientMetadata: &http.Client{Transport: costReportCancelTransport{}}},
	}
	registerCostReportCommands(root)
	ConfigureCommandErrors(root)
	err := root.Run(t.Context(), []string{"openai", "costs", "report", "--from", "2026-10-01", "--to", "2026-10-08"})
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF, "SIGINT must retain the joined transport failure")
	var exit cli.ExitCoder
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 130, exit.ExitCode())
}

type costReportCancelTransport struct{}

func (costReportCancelTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: costReportCancelBody{ctx: request.Context()}, Request: request,
	}, nil
}

type costReportCancelBody struct{ ctx context.Context }

func (costReportCancelBody) Close() error { return nil }

func (body costReportCancelBody) Read([]byte) (int, error) {
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		return 0, err
	}
	defer process.Release()
	if err := process.Signal(os.Interrupt); err != nil {
		return 0, err
	}
	<-body.ctx.Done()
	return 0, errors.Join(body.ctx.Err(), io.ErrUnexpectedEOF)
}
