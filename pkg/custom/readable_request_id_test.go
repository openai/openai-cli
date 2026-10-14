package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func requestIDError(t *testing.T, status int, id string) *openai.Error {
	t.Helper()
	header := make(http.Header)
	header.Set("X-Request-ID", id)
	apierr := &openai.Error{StatusCode: status, Response: &http.Response{Header: header}}
	require.NoError(t, json.Unmarshal([]byte(`{"message":"synthetic body detail","type":"server_error","future_field":{"sequence":9007199254740993}}`), apierr))
	return apierr
}

func requestIDErrorCommand(t *testing.T, args ...string) *cli.Command {
	t.Helper()
	root := &cli.Command{
		Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, HideHelpCommand: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"},
			&cli.StringFlag{Name: "format-error", Value: "auto"},
			&cli.StringFlag{Name: "transform-error"},
			&cli.BoolFlag{Name: "quiet"},
		},
		Action: func(context.Context, *cli.Command) error { return nil },
	}
	require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, args...)))
	return root
}

func TestReadableAPIRequestIDPlacementAndSelectedError(t *testing.T) {
	for _, wrapper := range []string{"direct", "wrapped", "joined", "joined exit"} {
		t.Run(wrapper, func(t *testing.T) {
			apierr := requestIDError(t, http.StatusInternalServerError, "req_abc123")
			var failure error = apierr
			switch wrapper {
			case "wrapped":
				failure = fmt.Errorf("synthetic-private-wrapper: %w", apierr)
			case "joined":
				failure = errors.Join(errors.New("synthetic-private-wrapper"), apierr)
			case "joined exit":
				failure = errors.Join(cli.Exit("synthetic-private-exit", 27), apierr)
			}
			var out bytes.Buffer
			require.NoError(t, ShowCommandError(requestIDErrorCommand(t), failure, &out))
			require.True(t, strings.HasPrefix(out.String(), "HTTP 500: Internal Server Error.\nRequest ID: req_abc123\nThe API is temporarily unavailable.\n"), out.String())
			require.Equal(t, 1, strings.Count(out.String(), "Request ID:"))
			require.NotContains(t, out.String(), "synthetic-private")
			require.NotContains(t, out.String(), "synthetic body detail")
			require.ErrorIs(t, failure, apierr)
			require.Equal(t, http.StatusInternalServerError, apierr.StatusCode)
			if wrapper == "joined exit" {
				var exit cli.ExitCoder
				require.ErrorAs(t, failure, &exit)
				require.Equal(t, 27, exit.ExitCode())
			}
		})
	}
	first := requestIDError(t, http.StatusForbidden, "first.opaque-ID_42")
	second := requestIDError(t, http.StatusInternalServerError, "second-identifier")
	for _, pair := range [][2]*openai.Error{{first, second}, {second, first}} {
		var out bytes.Buffer
		require.NoError(t, ShowCommandError(requestIDErrorCommand(t), errors.Join(pair[0], pair[1]), &out))
		want := fmt.Sprintf("HTTP %d: %s.\nRequest ID: %s\n", pair[0].StatusCode, http.StatusText(pair[0].StatusCode), pair[0].Response.Header.Get("X-Request-ID"))
		require.True(t, strings.HasPrefix(out.String(), want), out.String())
		require.NotContains(t, out.String(), pair[1].Response.Header.Get("X-Request-ID"))
	}
}

func TestReadableAPIRequestIDValidationAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		accepted bool
	}{
		{"prefixed", "req_abc123", true},
		{"opaque", "opaque.ID-42_value", true},
		{"UUID", "d25f3504-5ef8-408c-a5d5-0349c29c899a", true},
		{"256 bytes", strings.Repeat("a", 256), true},
		{"257 bytes", strings.Repeat("a", 257), false},
		{"empty", "", false},
		{"leading space", " req_abc123", false},
		{"trailing space", "req_abc123 ", false},
		{"newline", "req_abc123\nforged line", false},
		{"carriage return", "req_abc123\rforged", false},
		{"tab", "req_abc123\tforged", false},
		{"escape", "req_abc123\x1b[2J", false},
		{"NUL", "req_abc123\x00", false},
		{"bidi", "req_abc123\u202e", false},
		{"Unicode", "req_é", false},
		{"invalid UTF-8", "req_\xff", false},
		{"URL", "https://synthetic.invalid/?token=private", false},
		{"path", "/synthetic/private/file", false},
		{"assignment", "token=synthetic-private", false},
		{"colon", "req:abc123", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := requestIDErrorCommand(t)
			var baseline, output bytes.Buffer
			require.NoError(t, ShowCommandError(root, requestIDError(t, http.StatusBadRequest, ""), &baseline))
			require.NoError(t, ShowCommandError(root, requestIDError(t, http.StatusBadRequest, tc.id), &output))
			if !tc.accepted {
				require.Equal(t, baseline.String(), output.String(), "invalid IDs must be omitted entirely")
				return
			}
			line := "Request ID: " + tc.id + "\n"
			require.True(t, strings.HasPrefix(output.String(), "HTTP 400: Bad Request.\n"+line), output.String())
			require.Equal(t, baseline.String(), strings.Replace(output.String(), line, "", 1))
		})
	}
}

func TestReadableAPIRequestIDNilAndMissingMetadata(t *testing.T) {
	require.Empty(t, readableAPIRequestID(nil))
	for _, apierr := range []*openai.Error{
		{}, {Request: &http.Request{}}, {Response: &http.Response{}},
		{Response: &http.Response{Header: http.Header{"Unrelated-Id": {"synthetic-private"}}}},
	} {
		require.Empty(t, readableAPIRequestID(apierr))
	}
	var out bytes.Buffer
	require.NoError(t, ShowCommandError(requestIDErrorCommand(t), nil, &out))
	require.Empty(t, out.String())
	// Only the API error's actual Request governs reflection, not Response.Request.
	apierr := requestIDError(t, http.StatusBadRequest, "req_returned")
	apierr.Response.Request = &http.Request{Header: http.Header{"X-Request-Id": {"different-request"}}}
	require.Equal(t, "req_returned", readableAPIRequestID(apierr))
	apierr.Request = &http.Request{}
	require.Equal(t, "req_returned", readableAPIRequestID(apierr))
}

func TestReadableAPIRequestIDSuppressesCallerHeaderReflection(t *testing.T) {
	for _, key := range []string{"X-Request-ID", "x-request-id", "x-ReQuEsT-Id"} {
		for _, values := range [][]string{nil, {""}, {"synthetic-private-header"}} {
			apierr := requestIDError(t, http.StatusBadRequest, "req_returned")
			apierr.Request = &http.Request{Header: http.Header{key: values}}
			var baseline, output bytes.Buffer
			root := requestIDErrorCommand(t)
			require.NoError(t, ShowCommandError(root, requestIDError(t, http.StatusBadRequest, ""), &baseline))
			require.NoError(t, ShowCommandError(root, apierr, &output))
			require.Equal(t, baseline.String(), output.String(), "request header key %q must suppress the returned ID", key)
		}
	}
	apierr := requestIDError(t, http.StatusBadRequest, "req_returned")
	request, err := http.NewRequest(http.MethodGet, "https://synthetic-private.invalid/?token=synthetic-private", nil)
	require.NoError(t, err)
	request.Header = http.Header{
		"Authorization": {"Bearer synthetic-private"}, "Cookie": {"synthetic-private"},
		"X-Request-ID-Other": {"synthetic-private"},
	}
	apierr.Request = request
	apierr.Response.Header.Set("Set-Cookie", "synthetic-private")
	apierr.Response.Header.Set("Location", "https://synthetic-private.invalid")
	var out bytes.Buffer
	require.NoError(t, ShowCommandError(requestIDErrorCommand(t), apierr, &out))
	require.Contains(t, out.String(), "Request ID: req_returned\n")
	require.NotContains(t, out.String(), "synthetic-private")
}

func TestReadableAPIRequestIDQuietAndMachinePayloads(t *testing.T) {
	apierr := requestIDError(t, http.StatusBadRequest, "req_header_only")
	var ordinary, quiet bytes.Buffer
	require.NoError(t, ShowCommandError(requestIDErrorCommand(t), apierr, &ordinary))
	quietRoot := requestIDErrorCommand(t, "--quiet")
	require.True(t, quietRoot.Bool("quiet"))
	require.NoError(t, ShowCommandError(quietRoot, apierr, &quiet))
	require.Equal(t, ordinary.String(), quiet.String())
	require.Contains(t, quiet.String(), "Request ID: req_header_only\n")
	for _, args := range [][]string{
		{"--format-error", "json"}, {"--format-error", "jsonl"}, {"--format-error", "yaml"},
		{"--format-error", "raw"}, {"--format-error", "pretty"}, {"--format-error", "explore"},
		{"--format", "json"}, {"--quiet", "--format-error", "json"},
		{"--transform-error", "message"}, {"--format-error", "text", "--transform-error", "message"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := requestIDErrorCommand(t, args...)
			var baseline, output bytes.Buffer
			require.NoError(t, ShowCommandError(root, requestIDError(t, http.StatusBadRequest, ""), &baseline))
			require.NoError(t, ShowCommandError(root, apierr, &output))
			require.Equal(t, baseline.String(), output.String(), "returned metadata must not alter selected error data")
			require.NotContains(t, output.String(), "req_header_only")
		})
	}
}

func TestReadableAPIRequestIDCancellationAndSinkFailures(t *testing.T) {
	apierr := requestIDError(t, http.StatusBadRequest, "req_abc123")
	for _, failure := range []error{
		errors.Join(apierr, context.Canceled),
		fmt.Errorf("synthetic-private: %w", errors.Join(context.Canceled, apierr)),
	} {
		var out bytes.Buffer
		require.NoError(t, ShowCommandError(requestIDErrorCommand(t), failure, &out))
		require.Equal(t, "Request canceled.\n", out.String())
		require.ErrorIs(t, failure, context.Canceled)
		require.ErrorIs(t, failure, apierr)
	}
	cause := errors.New("synthetic diagnostic sink failure")
	for _, args := range [][]string{nil, {"--quiet"}, {"--format-error", "json"}} {
		root := requestIDErrorCommand(t, args...)
		require.ErrorIs(t, ShowCommandError(root, apierr, errorSink{cause}), cause)
		require.ErrorIs(t, ShowCommandError(root, apierr, shortErrorSink{}), io.ErrShortWrite)
	}
}
