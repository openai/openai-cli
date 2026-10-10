package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestFineTuningEmptyResultBoundaries(t *testing.T) {
	opts := ShowJSONOpts{Operation: "(resource) fine_tuning.jobs > (method) list", OutputKind: OutputPageItem}
	for _, tc := range []struct {
		name string
		edit func(*ShowJSONOpts)
	}{
		{"error", func(o *ShowJSONOpts) { o.Operation = "" }},
		{"events", func(o *ShowJSONOpts) { o.Operation = "(resource) fine_tuning.jobs > (method) list_events" }},
		{"response", func(o *ShowJSONOpts) { o.OutputKind = OutputResponse }},
		{"stream", func(o *ShowJSONOpts) { o.OutputKind = OutputStreamEvent }},
		{"extract", func(o *ShowJSONOpts) { o.Transform = "id" }},
		{"raw output", func(o *ShowJSONOpts) { o.RawOutput = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := opts
			tc.edit(&other)
			_, handled := fineTuningEmptyResult(other)
			require.False(t, handled)
		})
	}
	var out bytes.Buffer
	opts.Stdout = &out
	require.NoError(t, ShowJSONIterator(&transformTestIterator{}, -1, opts))
	require.Equal(t, "No fine-tuning jobs returned.\nTraining eligibility was not checked.\n", out.String())
	for _, format := range []string{"json", "jsonl", "yaml", "raw", "pretty"} {
		out.Reset()
		opts.Format = format
		require.NoError(t, ShowJSONIterator(&transformTestIterator{}, -1, opts))
		require.NotContains(t, out.String(), "Training eligibility")
	}
	opts.Format = "text"
	out.Reset()
	failure := errors.New("upstream failed")
	require.ErrorIs(t, ShowJSONIterator(&transformTestIterator{err: failure}, -1, opts), failure)
	require.Empty(t, out.String())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opts.Context = ctx
	require.ErrorIs(t, ShowJSONIterator(&transformTestIterator{}, -1, opts), context.Canceled)
	require.Empty(t, out.String())
	opts.Context = context.Background()
	opts.Stdout = failOutputWriter{err: io.ErrClosedPipe}
	require.ErrorIs(t, ShowJSONIterator(&transformTestIterator{}, -1, opts), io.ErrClosedPipe)
	iter := &transformTestIterator{}
	require.NoError(t, ShowJSONIterator(iter, 0, opts))
	require.Zero(t, iter.calls)
}

func TestFineTuningHelpCoversClonesAndPreservesDescriptions(t *testing.T) {
	jobs := &cli.Command{Name: "fine-tuning:jobs", Category: "API RESOURCE", Commands: []*cli.Command{
		{Name: "list", Description: "Existing guidance."},
		{Name: "retrieve"}, {Name: "list-events"}, {Name: "pause"}, {Name: "resume"},
	}}
	root := &cli.Command{Commands: []*cli.Command{jobs}}
	configureCommandSubgroups(root)
	configureFineTuningHelpContent(root)
	configureFineTuningHelpContent(root)
	for _, group := range []*cli.Command{jobs, root.Command("fine-tuning").Command("jobs")} {
		content := group.Command("list").Metadata["help-content"].(clihelp.Content)
		require.Contains(t, content.Description, "Existing guidance.")
		require.Equal(t, 1, bytes.Count([]byte(content.Description), []byte("Existing guidance.")))
		require.Contains(t, content.Description, "does not establish training eligibility")
		require.NotNil(t, group.Command("pause"))
		require.Nil(t, group.Command("export"))
	}
}
