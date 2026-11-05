package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/debugmiddleware"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestDebugCaptureOptionErrorsRemainSafe(t *testing.T) {
	const private = "synthetic-private-rejected-value\x1b[2J"
	failure := &debugCaptureOptionError{message: "--debug-body requires --debug"}
	const fallback = "The command could not be completed. Check your arguments with --help."
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"direct", failure, failure.Error()},
		{"wrapped", fmt.Errorf("%s: %w", private, failure), failure.Error()},
		{"joined", errors.Join(errors.New(private), failure), failure.Error()},
		{"canceled", errors.Join(failure, context.Canceled), "Request canceled."},
		{"untyped lookalike", errors.New(failure.Error()), fallback},
		{"private suffix", errors.New(failure.Error() + " " + private), fallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := localErrorMessage(nil, tc.err)
			require.Equal(t, tc.want, got)
			require.NotContains(t, got, "synthetic-private")
			require.NotContains(t, got, "\x1b")
		})
	}
}

func TestDebugCaptureOptionsDefaultsAndExplicitValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want debugmiddleware.CaptureOptions
	}{
		{"ordinary", nil, debugmiddleware.CaptureOptions{MaxBytes: 16384, MaxEvents: 64}},
		{"metadata debug", []string{"--debug"}, debugmiddleware.CaptureOptions{MaxBytes: 16384, MaxEvents: 64}},
		{"false without debug", []string{"--debug-body=false", "--debug-sse=false", "--debug-event-timing=false"}, debugmiddleware.CaptureOptions{MaxBytes: 16384, MaxEvents: 64}},
		{"explicitly disabled debug", []string{"--debug=false", "--debug-body=false", "--debug-sse=false", "--debug-event-timing=false"}, debugmiddleware.CaptureOptions{MaxBytes: 16384, MaxEvents: 64}},
		{"body", []string{"--debug", "--debug-body"}, debugmiddleware.CaptureOptions{Bodies: true, MaxBytes: 16384, MaxEvents: 64}},
		{"SSE", []string{"--debug", "--debug-sse"}, debugmiddleware.CaptureOptions{SSEFrames: true, MaxBytes: 16384, MaxEvents: 64}},
		{"event timing", []string{"--debug", "--debug-event-timing"}, debugmiddleware.CaptureOptions{EventTiming: true, MaxBytes: 16384, MaxEvents: 64}},
		{"explicit false", []string{"--debug", "--debug-body=false", "--debug-sse=false", "--debug-event-timing=false"}, debugmiddleware.CaptureOptions{MaxBytes: 16384, MaxEvents: 64}},
		{"minimum caps", []string{"--debug", "--debug-max-bytes", "1", "--debug-max-events", "1"}, debugmiddleware.CaptureOptions{MaxBytes: 1, MaxEvents: 1}},
		{"maximum caps", []string{"--debug", "--debug-body", "--debug-sse", "--debug-event-timing", "--debug-max-bytes", "1048576", "--debug-max-events", "4096"}, debugmiddleware.CaptureOptions{Bodies: true, SSEFrames: true, EventTiming: true, MaxBytes: 1048576, MaxEvents: 4096}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			root := debugCaptureTestCommand(func(_ context.Context, command *cli.Command) error {
				called = true
				got, err := debugCaptureOptions(command)
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
				return nil
			})
			ConfigureCommand(root)
			args := append([]string{"openai"}, tc.args...)
			args = append(args, "chat:completions", "create")
			require.NoError(t, root.Run(t.Context(), args))
			require.True(t, called)
		})
	}
}

func TestDebugCaptureOptionsInheritedPlacementAndClones(t *testing.T) {
	for _, route := range [][]string{{"chat:completions", "create"}, {"chat", "completions", "create"}} {
		for position := 0; position <= len(route); position++ {
			t.Run(fmt.Sprintf("%s/position=%d", strings.Join(route, "/"), position), func(t *testing.T) {
				called := false
				root := debugCaptureTestCommand(func(_ context.Context, command *cli.Command) error {
					called = true
					got, err := debugCaptureOptions(command)
					require.NoError(t, err)
					require.Equal(t, debugmiddleware.CaptureOptions{Bodies: true, SSEFrames: true, EventTiming: true, MaxBytes: 2048, MaxEvents: 8}, got)
					require.True(t, command.Root().Bool("raw-output"))
					return nil
				})
				ConfigureCommand(root)
				args := append([]string{"openai"}, route[:position]...)
				args = append(args, "--debug", "--debug-body", "--debug-sse", "--debug-event-timing",
					"--debug-max-bytes", "2048", "--debug-max-events", "8", "--raw-output")
				args = append(args, route[position:]...)
				require.NoError(t, root.Run(t.Context(), args))
				require.True(t, called)
			})
		}
	}
}

func TestDebugCaptureOptionsRejectBeforeTransport(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--debug-body"}, "--debug-body requires --debug"},
		{[]string{"--debug-sse"}, "--debug-sse requires --debug"},
		{[]string{"--debug-event-timing"}, "--debug-event-timing requires --debug"},
		{[]string{"--debug-max-bytes", "16384"}, "--debug-max-bytes requires --debug"},
		{[]string{"--debug-max-events", "64"}, "--debug-max-events requires --debug"},
		{[]string{"--debug-body=false", "--debug-max-bytes", "16384"}, "--debug-max-bytes requires --debug"},
		{[]string{"--debug-event-timing=false", "--debug-max-events", "64"}, "--debug-max-events requires --debug"},
		{[]string{"--debug=false", "--debug-body"}, "--debug-body requires --debug"},
		{[]string{"--debug", "--debug-max-bytes", "0"}, "--debug-max-bytes must be between 1 and 1048576"},
		{[]string{"--debug", "--debug-max-bytes", "-1"}, "--debug-max-bytes must be between 1 and 1048576"},
		{[]string{"--debug", "--debug-max-bytes", "1048577"}, "--debug-max-bytes must be between 1 and 1048576"},
		{[]string{"--debug", "--debug-max-events", "0"}, "--debug-max-events must be between 1 and 4096"},
		{[]string{"--debug", "--debug-max-events", "-1"}, "--debug-max-events must be between 1 and 4096"},
		{[]string{"--debug", "--debug-max-events", "4097"}, "--debug-max-events must be between 1 and 4096"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)
			beforeCalled, actionCalled := false, false
			root := debugCaptureTestCommand(func(ctx context.Context, _ *cli.Command) error {
				actionCalled = true
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
				if err != nil {
					return err
				}
				response, err := server.Client().Do(request)
				if response != nil {
					response.Body.Close()
				}
				return err
			})
			root.Before = func(ctx context.Context, _ *cli.Command) (context.Context, error) {
				beforeCalled = true
				return ctx, nil
			}
			ConfigureCommand(root)
			args := append([]string{"openai", "chat", "completions", "create"}, tc.args...)
			err := root.Run(t.Context(), args)
			require.ErrorContains(t, err, tc.want)
			require.False(t, beforeCalled)
			require.False(t, actionCalled)
			require.Zero(t, requests.Load())
		})
	}
}

func TestDebugCaptureOptionsRejectBeforeInjectedStdin(t *testing.T) {
	t.Setenv("OPENAI_UNTRUSTED_STDIN", "false")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{"requires debug", "--debug-body requires --debug", []string{"--debug-body"}},
		{"invalid byte limit", "--debug-max-bytes must be between 1 and 1048576", []string{"--debug", "--debug-max-bytes", "0"}},
		{"invalid event limit", "--debug-max-events must be between 1 and 4096", []string{"--debug", "--debug-max-events", "0"}},
		{"valid options reach stdin", "", []string{"--debug", "--debug-body"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &debugCaptureCountingReader{err: errors.New("synthetic stdin read failure")}
			called := false
			root := debugCaptureTestCommand(func(_ context.Context, command *cli.Command) error {
				called = true
				options, err := flagOptionsWithStdin(command, apiquery.NestedQueryFormatBrackets,
					apiquery.ArrayQueryFormatBrackets, ApplicationJSON, false, source)
				if tc.want == "" {
					require.ErrorIs(t, err, source.err)
					require.Positive(t, source.reads)
				} else {
					var optionError *debugCaptureOptionError
					require.ErrorAs(t, err, &optionError)
					require.Equal(t, tc.want, optionError.Error())
					require.Nil(t, options)
					require.Zero(t, source.reads)
				}
				return nil
			})
			// Omit ConfigureCommand so root.Before cannot provide the validation.
			root.Flags = append(root.Flags, debugCaptureFlags()...)
			root.Commands[0].Commands[0].Flags = []cli.Flag{&requestflag.Flag[string]{Name: "input", BodyPath: "input"}}
			require.Nil(t, root.Before)
			args := append([]string{"openai"}, tc.args...)
			// Explicit stdin remains a consumer regardless of the process stdin type.
			args = append(args, "chat:completions", "create", "--input", "@-")
			require.NoError(t, root.Run(t.Context(), args))
			require.True(t, called)
		})
	}
}

type debugCaptureCountingReader struct {
	reads int
	err   error
}

func (reader *debugCaptureCountingReader) Read([]byte) (int, error) {
	reader.reads++
	return 0, reader.err
}

func TestDebugCaptureOptionsIgnoreEndpointFlagWithSameName(t *testing.T) {
	called := false
	root := debugCaptureTestCommand(func(_ context.Context, command *cli.Command) error {
		called = true
		require.True(t, command.Bool(debugBodyFlag))
		got, err := debugCaptureOptions(command)
		require.NoError(t, err)
		require.False(t, got.Bodies)
		return nil
	})
	root.Commands[0].Commands[0].Flags = []cli.Flag{&cli.BoolFlag{Name: debugBodyFlag, Local: true}}
	ConfigureCommand(root)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "chat:completions", "create", "--debug-body"}))
	require.True(t, called)
}

func TestDebugCaptureOptionsLegacyUnregisteredDefaults(t *testing.T) {
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{&cli.BoolFlag{Name: "debug"}},
		Action: func(_ context.Context, command *cli.Command) error {
			got, err := debugCaptureOptions(command)
			require.NoError(t, err)
			require.Equal(t, debugmiddleware.CaptureOptions{MaxBytes: 16384, MaxEvents: 64}, got)
			return nil
		},
	}
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--debug"}))
}

func TestDebugCaptureFlagsRemainDiscoverableAndIdempotent(t *testing.T) {
	var output bytes.Buffer
	root := debugCaptureTestCommand(func(context.Context, *cli.Command) error {
		t.Fatal("help must not run the action")
		return nil
	})
	root.Writer = &output
	ConfigureCommand(root)
	ConfigureCommand(root)
	args, _, err := ConfigureHelp(root, []string{"openai", "--help"})
	require.NoError(t, err)
	require.NoError(t, root.Run(t.Context(), args))
	for _, name := range []string{debugBodyFlag, debugSSEFlag, debugEventTimingFlag, debugMaxBytesFlag, debugMaxEventsFlag} {
		count := 0
		for _, flag := range root.Flags {
			if flag.Names()[0] == name {
				count++
				require.False(t, flag.(interface{ IsLocal() bool }).IsLocal())
			}
		}
		require.Equal(t, 1, count, name)
		require.Contains(t, output.String(), "--"+name)
	}
	for _, text := range []string{"Troubleshooting", "Requires --debug", "16384", "1048576", "4096", "decoded event or output delivery"} {
		require.Contains(t, strings.Join(strings.Fields(output.String()), " "), text)
	}
	groups := root.Command("chat").Command("completions").Command("create").Metadata["help-flag-groups"].([]clihelp.FlagGroup)
	for _, group := range groups {
		if group.Title == "Troubleshooting" {
			require.Same(t, root, group.Owner)
			require.Equal(t, []string{"debug", debugBodyFlag, debugSSEFlag, debugEventTimingFlag, debugMaxBytesFlag, debugMaxEventsFlag}, group.Names)
			return
		}
	}
	t.Fatal("nested help lost the Troubleshooting flag group")
}

func debugCaptureTestCommand(action cli.ActionFunc) *cli.Command {
	return &cli.Command{
		Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags:    []cli.Flag{&cli.BoolFlag{Name: "debug"}, &cli.BoolFlag{Name: "raw-output"}},
		Commands: []*cli.Command{{Name: "chat:completions", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "create", Action: action}}}},
	}
}
