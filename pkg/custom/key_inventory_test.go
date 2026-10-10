package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func keyInventoryTestOptions(out io.Writer) ShowJSONOpts {
	return ShowJSONOpts{
		Context:    context.WithValue(context.Background(), keyInventoryScopeKey{}, func() string { return "proj_demo\x1b" }),
		Operation:  "(resource) admin.organization.projects.api_keys > (method) list",
		OutputKind: OutputPageItem, Stdout: out, Stderr: io.Discard,
	}
}

func TestKeyInventoryReadableOutput(t *testing.T) {
	var out bytes.Buffer
	opts := keyInventoryTestOptions(&out)
	value := gjson.Parse(`{"id":"key_demo","name":"test\u001b","last_used_at":null,"owner_project_access":"inactive","future":null}`)
	require.NoError(t, ShowJSON(value, opts))
	require.Contains(t, out.String(), "Project API key · proj_demo\\u001b\n")
	require.Contains(t, out.String(), "Name: test\\u001b\n")
	require.Contains(t, out.String(), "Last used at: (null)\n")
	require.Contains(t, out.String(), "Future: (null)\n")
	require.NotContains(t, out.String(), "Expires at:")
	require.NotContains(t, out.String(), "Status:")
}

func TestKeyInventoryEmptyAndFailure(t *testing.T) {
	var out bytes.Buffer
	opts := keyInventoryTestOptions(&out)
	require.NoError(t, ShowJSONIterator(&transformTestIterator{}, -1, opts))
	require.Equal(t, "No project API keys returned for proj_demo\\u001b.\n", out.String())
	failure := errors.New("synthetic upstream failure")
	out.Reset()
	require.ErrorIs(t, ShowJSONIterator(&transformTestIterator{err: failure}, -1, opts), failure)
	require.Empty(t, out.String())
}

func TestKeyInventoryWriteFailure(t *testing.T) {
	opts := keyInventoryTestOptions(failOutputWriter{})
	require.Error(t, ShowJSON(gjson.Parse(`{"id":"key_demo"}`), opts))
	ctx, cancel := context.WithCancel(opts.Context)
	cancel()
	opts.Context = ctx
	require.ErrorIs(t, ShowJSON(gjson.Parse(`{"id":"key_demo"}`), opts), context.Canceled)
}

func TestKeyInventoryInvalidResponseDiagnostics(t *testing.T) {
	const private = "synthetic-secret /private/synthetic-path \x1b[2J"
	const guidance = "The API returned an invalid key inventory response.\nRetry this read command."
	failure := transformers.ErrInvalidKeyInventoryResponse
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"direct", failure, guidance},
		{"wrapped", fmt.Errorf("%s: %w", private, failure), guidance},
		{"joined", errors.Join(errors.New(private), failure), guidance},
		{"canceled", errors.Join(failure, context.Canceled), "Request canceled."},
		{"deadline", errors.Join(failure, context.DeadlineExceeded), "The request timed out. The API may have received it; check its status before repeating it."},
		{"network timeout", errors.Join(failure, &net.DNSError{Name: private, IsTimeout: true}), "The request timed out. The API may have received it; check its status before repeating it."},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				root := readableErrorTestCommand(t, "--format-error", format)
				var out bytes.Buffer
				require.NoError(t, ShowCommandError(root, test.err, &out))
				if format == "json" {
					var got map[string]string
					require.NoError(t, json.Unmarshal(out.Bytes(), &got))
					require.Equal(t, map[string]string{"message": test.want}, got)
				} else {
					require.Equal(t, test.want+"\n", out.String())
				}
				require.NotContains(t, out.String(), "synthetic-secret")
				require.NotContains(t, out.String(), "/private/")
				require.NotContains(t, out.String(), "\x1b")
				require.ErrorIs(t, test.err, failure)
			})
		}
	}
	writeFailure := errors.New("synthetic diagnostic sink failure")
	require.ErrorIs(t, ShowCommandError(readableErrorTestCommand(t), failure, failOutputWriter{writeFailure}), writeFailure)
}
