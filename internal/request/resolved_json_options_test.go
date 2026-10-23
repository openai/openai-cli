package request

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestFlagOptionsResolvedJSONOptionsPreserveOrderAndWirePrecedence(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "false")
	failure := errors.New("synthetic validation failure")
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "rejected"}[reject], func(t *testing.T) {
			var events []string
			// Spare capacity exposes accidental mutation of the caller's observer slice.
			observers := make([]func([]byte), 1, 4)
			observers[0] = func([]byte) { events = append(events, "observer") }
			for _, value := range []string{"first", "second"} {
				events = nil
				dir := t.TempDir()
				bodyPath, headerPath := filepath.Join(dir, "body.json"), filepath.Join(dir, "header.txt")
				body := `{"stream":true,"value":"` + value + `"}`
				require.NoError(t, os.WriteFile(bodyPath, []byte(value), 0600))
				require.NoError(t, os.WriteFile(headerPath, []byte(value), 0600))
				stream := &requestflag.Flag[bool]{Name: "stream", BodyPath: "stream"}
				root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
					Flags: []cli.Flag{NewRequestHeaderFlag()},
					Commands: []*cli.Command{{Name: "create", Flags: []cli.Flag{
						&requestflag.Flag[map[string]any]{Name: "body", BodyRoot: true},
						stream,
						&requestflag.Flag[string]{Name: "trace", HeaderPath: "X-Trace"},
					}, Action: func(ctx context.Context, command *cli.Command) error {
						opts, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat,
							ApplicationJSON, true, strings.NewReader(""), PreparationHooks{
								ResolvedJSONOptions: func(parameters map[string]any, headers http.Header, encoded []byte) []option.RequestOption {
									events = append(events, "options")
									require.Equal(t, true, stream.Get())
									require.Equal(t, value, parameters["X-Trace"])
									require.Equal(t, "literal", headers.Get("X-Trace"))
									require.JSONEq(t, body, string(encoded))
									return []option.RequestOption{option.WithHeader("X-Trace", "callback"), option.WithHeader("X-Resolved", value)}
								},
								Encoding: EncodingHooks{ValidateJSON: func([]byte) error {
									events = append(events, "validate")
									require.False(t, stream.IsSet())
									if reject {
										return failure
									}
									return nil
								}, ObserveJSON: observers},
							})
						if err != nil {
							return err
						}
						client := openai.NewClient(option.WithAPIKey("synthetic-key"), option.WithBaseURL("https://synthetic.invalid/"),
							option.WithHTTPClient(&http.Client{Transport: defaultOptionsTransport(func(request *http.Request) (*http.Response, error) {
								events = append(events, "transport")
								require.Equal(t, "literal", request.Header.Get("X-Trace"))
								require.Equal(t, value, request.Header.Get("X-Resolved"))
								encoded, err := io.ReadAll(request.Body)
								require.NoError(t, err)
								require.JSONEq(t, body, string(encoded))
								return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
							})}))
						return client.Post(ctx, "synthetic", nil, nil, opts...)
					}}},
				}
				input := `{"stream":true,"value":"@` + bodyPath + `"}`
				err := root.Run(t.Context(), []string{"openai", "--header", "X-Trace: literal", "create", "--body", input, "--trace", "@" + headerPath})
				if reject {
					require.ErrorIs(t, err, failure)
					require.Equal(t, []string{"validate"}, events)
				} else {
					require.NoError(t, err)
					require.Equal(t, []string{"validate", "options", "observer", "transport"}, events)
				}
			}
		})
	}
}
