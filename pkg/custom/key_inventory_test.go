package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

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
