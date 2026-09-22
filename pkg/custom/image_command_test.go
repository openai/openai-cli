package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestImageWorkflowPreparedRequestIsPerCommandAndConsumedOnce(t *testing.T) {
	imageWorkflowEmptyStdin(t)

	for _, failAction := range []bool{false, true} {
		name := "successful action"
		if failAction {
			name = "failed action"
		}
		t.Run(name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "description.txt")
			require.NoError(t, os.WriteFile(filename, []byte("synthetic original prompt"), 0o600))
			actionError := errors.New("synthetic generated action failure")
			var other *cli.Command
			other = imageWorkflowTestCommand(func(_ context.Context, command *cli.Command) error {
				options, err := imageWorkflowFlagOptions(command)
				require.NoError(t, err)
				require.JSONEq(t, `{"prompt":"synthetic second prompt"}`, imageWorkflowRequestJSON(t, options))
				return nil
			})
			first := imageWorkflowTestCommand(func(_ context.Context, command *cli.Command) error {
				// Preparation has already resolved the file. The generated action
				// must use that request, even if the source no longer exists.
				require.NoError(t, os.Remove(filename))
				// A nested command must prepare and consume its own request without
				// stealing or replacing the first command's pending options.
				require.NoError(t, other.Run(t.Context(), []string{"generate", "--prompt", "synthetic second prompt"}))
				options, err := imageWorkflowFlagOptions(command)
				require.NoError(t, err)
				require.JSONEq(t, `{"prompt":"synthetic original prompt"}`, imageWorkflowRequestJSON(t, options))

				options, err = imageWorkflowFlagOptions(command)
				require.ErrorContains(t, err, "preparation does not match")
				require.Nil(t, options, "a second consumer must never receive reusable options")
				if failAction {
					return actionError
				}
				return nil
			})
			err := first.Run(t.Context(), []string{"generate", "--prompt", "@" + filename})
			if failAction {
				require.ErrorIs(t, err, actionError)
			} else {
				require.NoError(t, err)
			}
			for _, command := range []*cli.Command{first, other} {
				_, remains := command.Metadata[imageRequestMetadata]
				require.False(t, remains, "prepared requests must be released after the action")
			}
		})
	}
}

func TestImageWorkflowActionErrorRestoresAutomaticStreaming(t *testing.T) {
	imageWorkflowEmptyStdin(t)
	directory := t.TempDir()
	actionError := errors.New("synthetic streaming action failure")
	stream := &requestflag.Flag[*bool]{Name: "stream", BodyPath: "stream", Default: requestflag.Ptr(false)}
	var output bytes.Buffer
	command := &cli.Command{
		Name: "generate", Writer: &output,
		Flags: []cli.Flag{
			&requestflag.Flag[string]{Name: "prompt", BodyPath: "prompt"},
			stream,
			&requestflag.Flag[*int64]{Name: "partial-images", BodyPath: "partial_images"},
			&cli.StringFlag{Name: "output-dir"},
			&cli.StringFlag{Name: "inline"},
		},
		Action: imageGenerateWorkflow(func(ctx context.Context, command *cli.Command) error {
			selected, ok := command.Value("stream").(*bool)
			require.True(t, ok)
			require.NotNil(t, selected)
			require.True(t, *selected, "partials must select the generated streaming action")
			options, err := imageWorkflowFlagOptions(command)
			require.NoError(t, err)
			body := gjson.Parse(imageWorkflowRequestJSON(t, options))
			require.True(t, body.Get("stream").Bool(), "the request and generated selector must agree")
			require.Equal(t, int64(1), body.Get("partial_images").Int())
			presentation, ok := imagePresentationFor(ShowJSONOpts{Context: ctx, Operation: transformers.ImageGenerateOperation, OutputKind: OutputStreamEvent}, OutputStreamEvent)
			require.True(t, ok)
			require.Equal(t, directory, presentation.plan.directory)
			require.Same(t, &output, presentation.writer)
			return actionError
		}),
	}
	err := command.Run(t.Context(), []string{"generate", "--prompt", "synthetic streamed image", "--partial-images", "1", "--output-dir", directory, "--inline", "off"})
	require.ErrorIs(t, err, actionError)
	original, ok := command.Value("stream").(*bool)
	require.True(t, ok)
	require.NotNil(t, original)
	require.False(t, *original)
	require.False(t, command.IsSet("stream"))
	require.False(t, stream.IsSet())
	_, remains := command.Metadata[imageRequestMetadata]
	require.False(t, remains, "a failed delegated action retained its prepared request")
	contents, err := json.Marshal(requestflag.ExtractRequestContents(command).Body)
	require.NoError(t, err)
	require.False(t, gjson.ParseBytes(contents).Get("stream").Exists(), "automatic streaming became an explicit user option")
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries, "request preparation left an output file after failure")
}

func TestImagePreparedRequestMismatchFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name        string
		nested      apiquery.NestedQueryFormat
		array       apiquery.ArrayQueryFormat
		body        BodyContentType
		ignoreStdin bool
	}{
		{"nested query encoding", apiquery.NestedQueryFormatDots, apiquery.ArrayQueryFormatBrackets, ApplicationJSON, false},
		{"array query encoding", apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatComma, ApplicationJSON, false},
		{"body encoding", apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, MultipartFormEncoded, false},
		{"stdin ownership", apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, ApplicationJSON, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := &cli.Command{
				Name: "generate",
				Metadata: map[string]any{imageRequestMetadata: &preparedImageRequest{
					options: []option.RequestOption{option.WithRequestBody("application/json", []byte(`{"prompt":"prepared synthetic prompt"}`))},
				}},
				// If the cache is accidentally bypassed, ordinary flag handling
				// attempts this missing source instead of rejecting the mismatch.
				Flags: []cli.Flag{&requestflag.Flag[string]{Name: "prompt", BodyPath: "prompt", Default: "@" + filepath.Join(t.TempDir(), "missing.txt")}},
			}
			options, err := FlagOptions(command, test.nested, test.array, test.body, test.ignoreStdin)
			require.ErrorContains(t, err, "preparation does not match")
			require.Nil(t, options)
			// A mismatched caller is rejected before consuming the pending request.
			options, err = imageWorkflowFlagOptions(command)
			require.NoError(t, err)
			require.JSONEq(t, `{"prompt":"prepared synthetic prompt"}`, imageWorkflowRequestJSON(t, options))
		})
	}
}

func TestImageStreamSelectionRestoresParsedFlag(t *testing.T) {
	for _, test := range []struct {
		name     string
		initial  *bool
		args     []string
		selected bool
	}{
		{"unset nil to true", nil, nil, true},
		{"unset default false to true", requestflag.Ptr(false), nil, true},
		{"explicit false to true", requestflag.Ptr(false), []string{"--stream", "false"}, true},
		{"explicit true to false", requestflag.Ptr(false), []string{"--stream", "true"}, false},
		{"unset nil stays false", nil, nil, false},
		{"explicit true stays true", requestflag.Ptr(false), []string{"--stream", "true"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			flag := &requestflag.Flag[*bool]{Name: "stream", BodyPath: "stream", Default: test.initial}
			command := &cli.Command{
				Name: "generate", Writer: io.Discard, Flags: []cli.Flag{flag},
				Action: func(_ context.Context, command *cli.Command) error {
					before, err := json.Marshal(requestflag.ExtractRequestContents(command).Body)
					require.NoError(t, err)
					original := command.Value("stream").(*bool)
					wasSet, flagWasSet := command.IsSet("stream"), flag.IsSet()
					restore, err := selectImageStream(command, test.selected)
					require.NoError(t, err)
					func() {
						defer restore()
						selected := command.Value("stream").(*bool)
						require.Equal(t, test.selected, selected != nil && *selected)
						if original != nil {
							// The old parsed value may still be referenced by other
							// request code; the temporary selection must not mutate it.
							if len(test.args) > 0 {
								require.Equal(t, test.args[1] == "true", *original)
							} else {
								require.Equal(t, *test.initial, *original)
							}
						}
					}()
					after, err := json.Marshal(requestflag.ExtractRequestContents(command).Body)
					require.NoError(t, err)
					require.Equal(t, before, after, "temporary selection changed the original request body")
					require.Equal(t, original, command.Value("stream"))
					require.Equal(t, wasSet, command.IsSet("stream"), "temporary selection made an omitted option explicit")
					require.Equal(t, flagWasSet, flag.IsSet())
					return nil
				},
			}
			require.NoError(t, command.Run(t.Context(), append([]string{"generate"}, test.args...)))
		})
	}
}

func TestImagePresentationRequiresMatchingContextOperationAndKind(t *testing.T) {
	plan := &imageOutputPlan{}
	var writer bytes.Buffer
	ctx := context.WithValue(t.Context(), imagePresentationKey{}, imagePresentation{plan, &writer})
	for _, test := range []struct {
		name      string
		context   context.Context
		operation string
		kind      OutputKind
		requested OutputKind
		want      bool
	}{
		{"response", ctx, transformers.ImageGenerateOperation, OutputResponse, OutputResponse, true},
		{"stream", ctx, transformers.ImageGenerateOperation, OutputStreamEvent, OutputStreamEvent, true},
		{"nil context", nil, transformers.ImageGenerateOperation, OutputResponse, OutputResponse, false},
		{"unrelated context", t.Context(), transformers.ImageGenerateOperation, OutputResponse, OutputResponse, false},
		{"transform permission alone", transformers.WithImageOutput(t.Context()), transformers.ImageGenerateOperation, OutputResponse, OutputResponse, false},
		{"missing plan", context.WithValue(t.Context(), imagePresentationKey{}, imagePresentation{writer: &writer}), transformers.ImageGenerateOperation, OutputResponse, OutputResponse, false},
		{"wrong context value", context.WithValue(t.Context(), imagePresentationKey{}, plan), transformers.ImageGenerateOperation, OutputResponse, OutputResponse, false},
		{"error operation", ctx, "", OutputResponse, OutputResponse, false},
		{"other operation", ctx, "images.edit", OutputResponse, OutputResponse, false},
		{"operation prefix", ctx, transformers.ImageGenerateOperation + ".other", OutputResponse, OutputResponse, false},
		{"response sent to stream", ctx, transformers.ImageGenerateOperation, OutputResponse, OutputStreamEvent, false},
		{"stream sent to response", ctx, transformers.ImageGenerateOperation, OutputStreamEvent, OutputResponse, false},
		{"page item", ctx, transformers.ImageGenerateOperation, OutputPageItem, OutputResponse, false},
		{"unspecified kind", ctx, transformers.ImageGenerateOperation, OutputUnspecified, OutputResponse, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			presentation, ok := imagePresentationFor(ShowJSONOpts{Context: test.context, Operation: test.operation, OutputKind: test.kind}, test.requested)
			require.Equal(t, test.want, ok)
			if test.want {
				require.Same(t, plan, presentation.plan)
				require.Same(t, &writer, presentation.writer)
			}
		})
	}
}

func imageWorkflowTestCommand(next cli.ActionFunc) *cli.Command {
	return &cli.Command{
		Name: "generate", Writer: io.Discard,
		Flags: []cli.Flag{
			&requestflag.Flag[string]{Name: "prompt", BodyPath: "prompt"},
			&requestflag.Flag[*bool]{Name: "stream", BodyPath: "stream"},
			&cli.StringFlag{Name: "format", Value: "json"},
		},
		Action: imageGenerateWorkflow(next),
	}
}

func imageWorkflowEmptyStdin(t *testing.T) {
	t.Helper()
	// An empty, owned input makes this independent of the shell running tests.
	input, err := os.Open(os.DevNull)
	require.NoError(t, err)
	originalStdin := os.Stdin
	os.Stdin = input
	t.Cleanup(func() { os.Stdin = originalStdin; input.Close() })
}

func imageWorkflowFlagOptions(command *cli.Command) ([]option.RequestOption, error) {
	return FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, ApplicationJSON, false)
}

type imageWorkflowHTTPClient struct{ requestBody []byte }

func (client *imageWorkflowHTTPClient) Do(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	client.requestBody = body
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"created":0,"data":[]}`)),
		Request:    request,
	}, nil
}

// Build a real SDK request using the returned options, but capture it entirely
// in memory. The HTTP client never opens a socket or contacts an API.
func imageWorkflowRequestJSON(t *testing.T, options []option.RequestOption) string {
	t.Helper()
	capture := &imageWorkflowHTTPClient{}
	client := openai.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL("https://image-test.invalid/"), option.WithHTTPClient(capture), option.WithMaxRetries(0))
	_, err := client.Images.Generate(t.Context(), openai.ImageGenerateParams{}, options...)
	require.NoError(t, err)
	require.NotEmpty(t, capture.requestBody)
	return string(capture.requestBody)
}
