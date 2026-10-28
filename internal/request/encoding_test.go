package request

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestEncodeOptionsPreservesWireValuesAndLiteralHeaderPrecedence(t *testing.T) {
	for _, test := range []struct {
		name                      string
		bodyType                  BodyContentType
		body                      any
		flags                     []cli.Flag
		wantBody, wantContentType string
	}{
		{name: "JSON object", bodyType: ApplicationJSON, body: map[string]any{"stream": false, "value": "synthetic"}, wantBody: `{"stream":false,"value":"synthetic"}`, wantContentType: "application/json"},
		{name: "JSON null", bodyType: ApplicationJSON, wantBody: "null", wantContentType: "application/json"},
		{name: "binary bytes", bodyType: ApplicationOctetStream, body: []byte{0, 1, 255}, wantBody: string([]byte{0, 1, 255}), wantContentType: "application/octet-stream"},
		{name: "binary string", bodyType: ApplicationOctetStream, body: "synthetic\x00body", wantBody: "synthetic\x00body", wantContentType: "application/octet-stream"},
		{name: "body root retains SDK body", bodyType: ApplicationOctetStream, body: make(chan int), flags: []cli.Flag{&requestflag.Flag[string]{Name: "body", BodyRoot: true}}, wantBody: "SDK body", wantContentType: "application/sdk"},
		{name: "empty body retains SDK body", bodyType: EmptyBody, body: make(chan int), wantBody: "SDK body", wantContentType: "application/sdk"},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, err := EncodeOptions(&cli.Command{Flags: test.flags}, requestflag.RequestContents{
				Queries: map[string]any{"filter": map[string]any{"name": "synthetic"}, "tag": []string{"a", "b"}, "empty": nil},
				Headers: map[string]any{"X-Trace": []string{"first", "second"}, "X-Meta": map[string]any{"name": "synthetic"}},
				Body:    test.body,
			}, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, test.bodyType, EncodingHooks{})
			require.NoError(t, err)
			options = append(options, option.WithHeader("X-Trace", "literal override"))
			calls := 0
			client := openai.NewClient(
				option.WithAPIKey("synthetic-key"), option.WithBaseURL("https://synthetic.invalid/"),
				option.WithRequestBody("application/sdk", []byte("SDK body")),
				option.WithHTTPClient(&http.Client{Transport: defaultOptionsTransport(func(request *http.Request) (*http.Response, error) {
					calls++
					require.Equal(t, []string{"a", "b"}, request.URL.Query()["tag"])
					require.Equal(t, "synthetic", request.URL.Query().Get("filter[name]"))
					require.Equal(t, []string{""}, request.URL.Query()["empty"])
					require.Equal(t, []string{"literal override"}, request.Header.Values("X-Trace"))
					require.Equal(t, "synthetic", request.Header.Get("X-Meta.name"))
					require.Equal(t, test.wantContentType, request.Header.Get("Content-Type"))
					body, err := io.ReadAll(request.Body)
					require.NoError(t, err)
					require.Equal(t, test.wantBody, string(body))
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
				})}),
			)
			require.NoError(t, client.Post(t.Context(), "synthetic", nil, nil, options...))
			require.Equal(t, 1, calls)
		})
	}
}

func TestEncodeOptionsJSONHookOrderAndFailure(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rejected"}[reject], func(t *testing.T) {
			stream := &requestflag.Flag[bool]{Name: "stream", BodyPath: "stream"}
			failure := errors.New("synthetic feature validation failure")
			var events []string
			options, err := EncodeOptions(&cli.Command{Flags: []cli.Flag{stream}}, requestflag.RequestContents{Body: map[string]any{"stream": true}}, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, ApplicationJSON, EncodingHooks{
				ValidateJSON: func(body []byte) error {
					require.Equal(t, `{"stream":true}`, string(body))
					require.False(t, stream.IsSet())
					events = append(events, "validate")
					if reject {
						return failure
					}
					return nil
				},
				ObserveJSON: []func([]byte){func(body []byte) {
					require.True(t, stream.IsSet())
					require.Equal(t, `{"stream":true}`, string(body))
					events = append(events, "first")
				}, func(body []byte) {
					require.Equal(t, `{"stream":true}`, string(body))
					events = append(events, "second")
				}},
			})
			if reject {
				require.ErrorIs(t, err, failure)
				require.Nil(t, options)
				require.False(t, stream.IsSet())
				require.Equal(t, []string{"validate"}, events)
			} else {
				require.NoError(t, err)
				require.Equal(t, []string{"validate", "first", "second"}, events)
			}
		})
	}
}

func TestEncodeOptionsMultipartHooksPreserveOwnershipAndReadTiming(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rejected"}[reject], func(t *testing.T) {
			stream := &requestflag.Flag[bool]{Name: "stream", BodyPath: "stream"}
			source := &encodingUpload{}
			body := map[string]any{"file": FileUpload{Reader: source, FileName: "synthetic.txt"}}
			failure := errors.New("synthetic multipart preparation failure")
			var observed io.Closer
			_, err := EncodeOptions(&cli.Command{Flags: []cli.Flag{stream}}, requestflag.RequestContents{Body: body}, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, MultipartFormEncoded, EncodingHooks{
				PrepareMultipart: func(body map[string]any) error {
					require.False(t, stream.IsSet())
					body["stream"] = true
					if reject {
						return failure
					}
					return nil
				},
				ObserveMultipart: func(body io.Closer) {
					require.True(t, stream.IsSet())
					observed = body
				},
			})
			require.Zero(t, source.reads)
			require.Zero(t, source.closes)
			if reject {
				require.ErrorIs(t, err, failure)
				require.Nil(t, observed)
				require.False(t, stream.IsSet())
				// The caller still owns cleanup when preparation rejects the body.
				require.NoError(t, CloseFileUploads(body))
			} else {
				require.NoError(t, err)
				require.NotNil(t, observed)
				require.NoError(t, observed.Close())
				require.NoError(t, observed.Close())
			}
			require.Equal(t, 1, source.closes)
		})
	}
}

type encodingUpload struct{ reads, closes int }

func (source *encodingUpload) Read([]byte) (int, error) { source.reads++; return 0, io.EOF }
func (source *encodingUpload) Close() error             { source.closes++; return nil }
