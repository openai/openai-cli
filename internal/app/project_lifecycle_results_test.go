package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const projectLifecycleFixture = `{"id":"proj_returned","object":"organization.project","name":"Renamed","status":"active"}`

func TestProjectRetrievePreservesRedirectedOutput(t *testing.T) {
	input := `{"object":"organization.project","id":"proj_demo","created_at":1700000000,"psp_decryption_key_id":null}`
	for _, format := range []string{"auto", "text", "json"} {
		var out bytes.Buffer
		opts := ShowJSONOpts{Context: t.Context(), Operation: "(resource) admin.organization.projects > (method) retrieve",
			OutputKind: OutputResponse, Format: format, ExplicitFormat: format != "auto", Stdout: &out}
		require.NoError(t, ShowJSON(gjson.Parse(input), opts))
		if format == "json" {
			require.JSONEq(t, input, out.String())
			continue
		}
		require.Contains(t, out.String(), "Created at: 1700000000")
		require.Contains(t, out.String(), "Psp decryption key ID: (null)")
	}
}

func TestProjectLifecycleReceipt(t *testing.T) {
	var out, diagnostics bytes.Buffer
	opts := ShowJSONOpts{Context: t.Context(), Operation: "(resource) admin.organization.projects > (method) update", OutputKind: OutputResponse, Stdout: &out, Stderr: &diagnostics}
	handled, err := showProjectLifecycleResult(gjson.Parse(projectLifecycleFixture), opts)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "Project proj_returned updated.\nName: Renamed\nStatus: active\n", out.String())
	require.Empty(t, diagnostics.String())

	out.Reset()
	opts.Operation = "(resource) admin.organization.projects > (method) archive"
	handled, err = showProjectLifecycleResult(gjson.Parse(strings.ReplaceAll(projectLifecycleFixture, "active", "archived")), opts)
	require.NoError(t, err)
	require.True(t, handled)
	require.Contains(t, diagnostics.String(), "Archive is not a delete operation.")
	require.NotContains(t, out.String(), "delete")
}

func TestProjectLifecyclePreservesExplicitModes(t *testing.T) {
	for _, format := range []string{"json", "jsonl", "yaml", "raw", "pretty", "explore"} {
		handled, err := showProjectLifecycleResult(gjson.Parse(projectLifecycleFixture), ShowJSONOpts{Format: format})
		require.NoError(t, err)
		require.False(t, handled)
	}
	for _, opts := range []ShowJSONOpts{
		{Format: "text", Transform: "id"}, {Format: "text", RawOutput: true},
		{Context: context.WithValue(t.Context(), outputPolicyKey{}, outputPolicy{quiet: true, diagnostics: true})},
	} {
		handled, err := showProjectLifecycleResult(gjson.Parse(projectLifecycleFixture), opts)
		require.NoError(t, err)
		require.False(t, handled)
	}
}

func TestProjectLifecycleEscapesAndPreservesFailures(t *testing.T) {
	var out bytes.Buffer
	opts := ShowJSONOpts{Context: t.Context(), Operation: "(resource) admin.organization.projects > (method) update", OutputKind: OutputResponse, Stdout: &out}
	value := gjson.Parse(strings.ReplaceAll(projectLifecycleFixture, "proj_returned", `proj_\u001b[2J\nSpoof`))
	_, err := showProjectLifecycleResult(value, opts)
	require.NoError(t, err)
	require.NotContains(t, out.String(), "\x1b")
	require.NotContains(t, out.String(), "\nSpoof")

	opts.Stdout = projectLifecycleFailWriter{}
	_, err = showProjectLifecycleResult(value, opts)
	require.ErrorIs(t, err, io.ErrClosedPipe)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	opts.Context = ctx
	handled, err := showProjectLifecycleResult(value, opts)
	require.True(t, handled)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, errors.Is(err, io.ErrClosedPipe))
}

type projectLifecycleFailWriter struct{}

func (projectLifecycleFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
