package custom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestImagePickerSelectionsRestoreRequestFlagState(t *testing.T) {
	app := &cli.Command{Name: "generate", Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "prompt", BodyPath: "prompt"},
		&requestflag.Flag[*string]{Name: "model", BodyPath: "model"},
		&requestflag.Flag[*int64]{Name: "n", Aliases: []string{"count"}, BodyPath: "n", Default: requestflag.Ptr[int64](1)},
	}}
	app.Action = func(ctx context.Context, command *cli.Command) error {
		before := requestflag.ExtractRequestContents(command)
		for range 2 {
			func() {
				for _, pair := range [][2]string{{"prompt", "a blue cat"}, {"model", "gpt-image-2"}, {"count", "2"}} {
					restore, err := setImagePickerFlag(command, pair[0], pair[1])
					require.NoError(t, err)
					defer restore()
				}
				require.Equal(t, map[string]any{"prompt": "a blue cat", "model": requestflag.Ptr("gpt-image-2"), "n": requestflag.Ptr[int64](2)}, requestflag.ExtractRequestContents(command).Body)
			}()
			require.Equal(t, before, requestflag.ExtractRequestContents(command), "a later action must not inherit picker selections")
			require.False(t, command.IsSet("model"))
			require.False(t, command.IsSet("n"))
			require.True(t, command.IsSet("prompt"))
			require.Equal(t, "original", command.String("prompt"))
		}
		_, err := setImagePickerFlag(command, "count", "invalid")
		require.Error(t, err)
		require.Equal(t, before, requestflag.ExtractRequestContents(command))
		_, err = setImagePickerFlag(command, "missing", "value")
		require.Error(t, err)
		return nil
	}
	require.NoError(t, app.Run(context.Background(), []string{"openai", "--prompt", "original"}))
}

func TestImagePickerCanceledContextDoesNotOpenTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := runImagePicker(ctx, nil, nil, imagePickerOptions{})
	require.True(t, errors.Is(err, context.Canceled))
	require.Empty(t, result.Args)
}

func TestImagePickerDraftRecovery(t *testing.T) {
	for _, failedFirst := range []bool{false, true} {
		t.Run(fmt.Sprint("failedFirst=", failedFirst), func(t *testing.T) {
			app := &cli.Command{Flags: []cli.Flag{
				&requestflag.Flag[string]{Name: "prompt", BodyPath: "prompt"},
				&cli.StringFlag{Name: "output-dir"},
			}}
			var diagnostics bytes.Buffer
			picks, requests := 0, 0
			first := imagePickerSettings{prompt: "A blue cat\nwith a hat 🐈", quality: "high", outputDir: t.TempDir()}
			second := first
			second.prompt += " in space"
			second.quality = "low"
			app.Action = func(ctx context.Context, command *cli.Command) error {
				return runImagePickerDrafts(ctx, command, func(requestCtx context.Context, _ *cli.Command) error {
					requests++
					want := first
					if requests == 2 {
						want = second
					}
					require.Equal(t, want.prompt, command.String("prompt"))
					require.Equal(t, want.outputDir, command.String("output-dir"))
					require.Equal(t, want.prompt, requestCtx.Value(imagePickerLoadingPromptKey{}))
					require.Nil(t, ctx.Value(imagePickerLoadingPromptKey{}), "request display state must not modify the parent context")
					if failedFirst && requests == 1 {
						return &openai.Error{StatusCode: 429}
					}
					return nil
				}, &diagnostics, func(options imagePickerOptions) (imagePickerResult, error) {
					picks++
					require.Nil(t, ctx.Value(imagePickerLoadingPromptKey{}), "a retry must not inherit the previous loading prompt")
					require.Equal(t, picks-1, requests, "reopening must not run an action")
					require.False(t, command.IsSet("prompt"))
					require.False(t, command.IsSet("output-dir"))
					if picks == 1 {
						require.Nil(t, options.initial)
						require.Empty(t, options.Prompt)
						require.False(t, options.resuming)
					} else {
						want := first
						if picks == 3 {
							want = second
						}
						require.Equal(t, want, *options.initial)
						require.Equal(t, want.prompt, options.Prompt)
						require.True(t, options.resuming)
						m, err := newImagePicker(options)
						require.NoError(t, err)
						require.Equal(t, want.prompt, string(m.draft))
						require.Empty(t, m.result.Args)
						if failedFirst && picks == 2 {
							require.Contains(t, options.initialNote, "prompt and settings are still here")
							require.Contains(t, diagnostics.String(), "Wait a moment before trying again")
						} else {
							require.Contains(t, options.initialNote, "All images saved")
						}
					}
					if picks == 3 {
						return imagePickerResult{PrintOnly: true}, nil
					}
					selection := first
					if picks == 2 {
						selection = second
					}
					return imagePickerResult{settings: selection, Args: []string{"images", "generate", "--prompt", selection.prompt, "--output-dir", selection.outputDir}}, nil
				})
			}
			require.NoError(t, app.Run(context.Background(), []string{"openai"}))
			require.Equal(t, 2, requests)
			require.Equal(t, 3, picks)
			if !failedFirst {
				require.Empty(t, diagnostics.String())
			}
		})
	}
}

func TestImagePickerRecoverableRequest(t *testing.T) {
	apiFailure := &openai.Error{StatusCode: 400}
	transportFailure := &url.Error{Op: "Post", URL: "https://example.invalid", Err: io.EOF}
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"API rejection", apiFailure, true},
		{"wrapped API rejection", fmt.Errorf("request: %w", apiFailure), true},
		{"transport failure", transportFailure, true},
		{"request timeout", context.DeadlineExceeded, true},
		{"canceled", context.Canceled, false},
		{"transport canceled", &url.Error{Err: context.Canceled}, false},
		{"interrupted", &imageLoadingInterrupt{context.Canceled}, false},
		{"exit status", cli.Exit("stop", 143), false},
		{"save failed", imageSavingFailure("save failed", transportFailure), false},
		{"output failed", &outputWriteError{transportFailure}, false},
		{"joined requests", errors.Join(apiFailure, transportFailure), true},
		{"joined output", errors.Join(apiFailure, io.ErrClosedPipe), false},
		{"joined cancellation", errors.Join(apiFailure, context.Canceled), false},
		{"unknown", errors.New("unknown failure"), false},
		{"nil", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, imagePickerRecoverableRequest(test.err))
		})
	}
}

func TestImagePickerRecoveryStopsOnTerminalFailure(t *testing.T) {
	requestFailure := &openai.Error{StatusCode: 500}
	for _, test := range []struct {
		name                 string
		actionErr, pickerErr error
		cancel               bool
		diagnostics          io.Writer
		want                 error
	}{
		{name: "save failure", actionErr: imageSavingFailure("saved only one image", io.ErrUnexpectedEOF), want: io.ErrUnexpectedEOF},
		{name: "picker failure", pickerErr: io.ErrClosedPipe, want: io.ErrClosedPipe},
		{name: "request cancellation", actionErr: &imageLoadingInterrupt{context.Canceled}, want: context.Canceled},
		{name: "parent cancellation", actionErr: requestFailure, cancel: true, want: requestFailure},
		{name: "diagnostic failure", actionErr: requestFailure, diagnostics: imagePickerFailedWriter{}, want: io.ErrClosedPipe},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			picks, requests := 0, 0
			diagnostics := test.diagnostics
			if diagnostics == nil {
				diagnostics = io.Discard
			}
			err := runImagePickerDrafts(ctx, &cli.Command{}, func(context.Context, *cli.Command) error {
				requests++
				if test.cancel {
					cancel()
				}
				return test.actionErr
			}, diagnostics, func(imagePickerOptions) (imagePickerResult, error) {
				picks++
				require.Equal(t, 1, picks, "terminal failures must not reopen the picker")
				return imagePickerResult{Args: []string{"images", "generate"}}, test.pickerErr
			})
			require.ErrorIs(t, err, test.want)
			if test.diagnostics != nil {
				require.ErrorIs(t, err, requestFailure)
			}
			require.Equal(t, 1, picks)
			if test.pickerErr != nil {
				require.Zero(t, requests)
			} else {
				require.Equal(t, 1, requests)
			}
		})
	}
}

type imagePickerFailedWriter struct{}

func (imagePickerFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestImagePickerDraftExitDoesNotRequest(t *testing.T) {
	for _, code := range []int{0, 130, 143} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			err := runImagePickerDrafts(context.Background(), &cli.Command{}, func(context.Context, *cli.Command) error {
				t.Fatal("cancellation must not start a request")
				return nil
			}, io.Discard, func(imagePickerOptions) (imagePickerResult, error) {
				return imagePickerResult{Canceled: true, ExitCode: code}, nil
			})
			var exit cli.ExitCoder
			require.ErrorAs(t, err, &exit)
			if code == 0 {
				code = 130
			}
			require.Equal(t, code, exit.ExitCode())
			require.Empty(t, exit.Error())
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runImagePickerDrafts(ctx, &cli.Command{}, nil, io.Discard, func(imagePickerOptions) (imagePickerResult, error) {
		t.Fatal("a canceled session must not open a picker")
		return imagePickerResult{}, nil
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestImagePickerRequestGuidanceKeepsPrivateDetailsOut(t *testing.T) {
	const private = "private-key.example/private-prompt\x1b]0;private-title\a"
	for _, test := range []struct {
		name   string
		status int
		code   string
		want   string
	}{
		{"request", 400, "unknown-private-code", "Try changing the prompt or settings"},
		{"settings", 422, "invalid_value", "Try changing the prompt or settings"},
		{"content", 400, "content_policy_violation", "image safety checks"},
		{"model code", 400, "model_not_found", "Try another model"},
		{"authentication", 401, "", "Setup guide: openai help setup"},
		{"permissions", 403, "", "Your key or project lacks permission"},
		{"model status", 404, "", "Try another model"},
		{"rate limit", 429, "", "Wait a moment"},
		{"billing", 429, "insufficient_quota", "Check your billing and limits"},
		{"request timeout", 408, "", "Check API usage"},
		{"gateway timeout", 504, "", "Check API usage"},
		{"server error", 500, "", "Check API usage"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := fmt.Errorf("private wrapper: %w", &openai.Error{StatusCode: test.status, Code: test.code, Message: private, Param: private})
			message := imagePickerRequestMessage(&cli.Command{}, err, false)
			require.Contains(t, message, test.want)
			for _, hidden := range []string{"private", "\x1b", "\a", "--format-error", "help --all", "HTTP"} {
				require.NotContains(t, message, hidden)
			}
		})
	}
	for _, test := range []struct {
		cause error
		want  string
	}{
		{context.DeadlineExceeded, "timed out"},
		{io.ErrUnexpectedEOF, "connection to the image service failed"},
	} {
		err := &url.Error{Op: "Post", URL: private, Err: test.cause}
		message := imagePickerRequestMessage(&cli.Command{}, err, false)
		require.Contains(t, message, test.want)
		require.NotContains(t, message, "private")
	}
}

func TestImagePickerCredentialGuidanceUsesCurrentExecutable(t *testing.T) {
	for _, invocation := range []string{"openai", "'/tmp/CLI trial/openai'"} {
		for _, status := range []int{401, 403} {
			root := &cli.Command{Metadata: map[string]any{"help-invocation": invocation}}
			message := imagePickerRequestMessage(root, fmt.Errorf("private wrapper: %w", &openai.Error{
				StatusCode: status, Message: "synthetic-private-key", Param: "synthetic-private-prompt",
			}), false)
			for _, want := range []string{
				"Copy your prompt. Ctrl+C exits.",
				"Setup guide: " + invocation + " help setup",
				"Reopen the picker in this shell.",
			} {
				require.Contains(t, message, want)
			}
			require.NotContains(t, message, "synthetic-private")
			require.NotContains(t, message, "Press Enter")
			require.NotContains(t, message, "models list")
			require.NotContains(t, message, "--api-key")
			require.LessOrEqual(t, len(strings.Split(message, "\n")), 6, "the primary error must stay compact")
			if invocation == "openai" {
				for _, line := range strings.Split(message, "\n") {
					require.LessOrEqual(t, len(line), 40, "ordinary guidance must fit the minimum terminal width")
				}
			}
			if status == 401 {
				require.Contains(t, message, "Authentication failed\n")
				require.Contains(t, message, "Your API key was not accepted.")
			} else {
				require.Contains(t, message, "Access denied\n")
				require.NotContains(t, message, "key was not accepted")
			}
		}
	}
}

func TestImagePickerRequestWriterKeepsPlainOutputSafe(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	root := &cli.Command{Metadata: map[string]any{"help-invocation": "openai\x1b]52;synthetic\a"}}
	var out bytes.Buffer
	err := writeImagePickerRequestMessage(t.Context(), &out, root, &openai.Error{StatusCode: 401, Message: "private-server-detail"}, false)
	require.NoError(t, err)
	require.Contains(t, out.String(), "Authentication failed")
	require.NotContains(t, out.String(), "\x1b")
	require.NotContains(t, out.String(), "\a")
	require.NotContains(t, out.String(), "private-server-detail")
	require.True(t, strings.HasSuffix(out.String(), "\n"))
	require.False(t, strings.HasSuffix(out.String(), "\n\n"))

	require.ErrorIs(t, writeImagePickerRequestMessage(t.Context(), imagePickerFailedWriter{}, root, &openai.Error{StatusCode: 401}, false), io.ErrClosedPipe)
	require.ErrorIs(t, writeImagePickerRequestMessage(t.Context(), imagePickerShortWriter{}, root, &openai.Error{StatusCode: 403}, false), io.ErrShortWrite)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out.Reset()
	require.ErrorIs(t, writeImagePickerRequestMessage(ctx, &out, root, &openai.Error{StatusCode: 401}, false), context.Canceled)
	require.Empty(t, out.String())
}

type imagePickerShortWriter struct{}

func (imagePickerShortWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestImagePickerCredentialFailureRetainsDraftWithoutSuggestingImmediateRetry(t *testing.T) {
	for _, status := range []int{401, 403} {
		for _, saved := range []bool{false, true} {
			t.Run(fmt.Sprintf("status=%d/saved=%t", status, saved), func(t *testing.T) {
				app := &cli.Command{ExitErrHandler: func(context.Context, *cli.Command, error) {}, Metadata: map[string]any{"help-invocation": "openai"}, Flags: []cli.Flag{
					&requestflag.Flag[string]{Name: "prompt", BodyPath: "prompt"},
				}}
				draft := imagePickerSettings{prompt: "A synthetic blue robot", count: "3", quality: "high"}
				var diagnostics bytes.Buffer
				picks, requests := 0, 0
				app.Action = func(ctx context.Context, command *cli.Command) error {
					return runImagePickerDrafts(ctx, command, func(context.Context, *cli.Command) error {
						requests++
						return &openai.Error{StatusCode: status}
					}, &diagnostics, func(options imagePickerOptions) (imagePickerResult, error) {
						picks++
						if picks == 1 {
							return imagePickerResult{settings: draft, draftSaved: saved, Args: []string{"images", "generate", "--prompt", draft.prompt}}, nil
						}
						require.Equal(t, 1, requests, "showing guidance must not check access or retry")
						require.Equal(t, draft, *options.initial)
						require.Equal(t, draft.prompt, options.Prompt)
						require.True(t, options.resuming)
						if saved {
							require.Equal(t, "Draft saved.", options.initialNote)
						} else {
							require.Equal(t, "Draft not saved. Copy before Ctrl+C.", options.initialNote)
						}
						require.NotContains(t, options.initialNote, "Enter")
						return imagePickerResult{Canceled: true}, nil
					})
				}
				err := app.Run(context.Background(), []string{"openai"})
				var exit cli.ExitCoder
				require.ErrorAs(t, err, &exit)
				require.Equal(t, 130, exit.ExitCode())
				require.Equal(t, 2, picks)
				require.Equal(t, 1, requests)
				require.Contains(t, diagnostics.String(), "Setup guide: openai help setup")
				if saved {
					require.Contains(t, diagnostics.String(), "Your draft is saved.")
					require.NotContains(t, diagnostics.String(), "Copy your prompt")
				} else {
					require.NotContains(t, diagnostics.String(), "Your draft is saved.")
				}
			})
		}
	}
}
