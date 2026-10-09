package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/stretchr/testify/require"
)

func staticModelIterator(t *testing.T, data string, maximum int64, initialError error) (*pagination.PageAutoPager[openai.Model], *outputIterator[openai.Model]) {
	t.Helper()
	var page pagination.Page[openai.Model]
	require.NoError(t, json.Unmarshal([]byte(`{"object":"list","data":`+data+`}`), &page))
	source := pagination.NewPageAutoPager(&page, initialError)
	return source, &outputIterator[openai.Model]{source: source, context: context.Background(), transform: transformers.Identity, remaining: maximum}
}

func TestModelsListViewerLoadedModelsAndLimit(t *testing.T) {
	source, iter := staticModelIterator(t, `[{"id":"model-first","object":"model","owned_by":"synthetic"},{"id":"model-second","object":"model","owned_by":"synthetic"},{"id":"unread","object":"model","owned_by":"synthetic"}]`, 2, nil)
	var out bytes.Buffer
	opts := ShowJSONOpts{Operation: "(resource) models > (method) list", OutputKind: OutputPageItem, Stdout: &out}
	opts.setDefaults()
	require.NoError(t, writeModelsList(iter, opts, 80, 0))
	require.Contains(t, out.String(), "Listed 2 models.")
	require.Contains(t, out.String(), "OWNER")
	require.Contains(t, out.String(), "model-first")
	require.Contains(t, out.String(), "model-second")
	require.NotContains(t, out.String(), "unread")
	require.Equal(t, 2, source.Index())
}

func TestModelsListViewerModelsTableOmitsOtherMetadata(t *testing.T) {
	for _, test := range []struct {
		name, data, want, hidden string
		width                    int
	}{
		{"narrow", `[{"id":"model-complete-long-identifier","object":"model","owned_by":"synthetic"}]`, "ID: model-complete-long-identifier\nOwned by: synthetic\n", "Created:", 10},
		{"unknown", `[{"id":"model-a","object":"model","owned_by":"synthetic","notice":"keep this"}]`, "model-a  synthetic\n", "keep this", 80},
		{"retirement", `[{"id":"model-a","object":"model","owned_by":"synthetic","shutdown_date":"2027-01-01"}]`, "model-a  synthetic\n", "2027-01-01", 80},
		{"empty", `[]`, "No results.\n", "Models", 80},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, iter := staticModelIterator(t, test.data, -1, nil)
			var out bytes.Buffer
			opts := ShowJSONOpts{Operation: "(resource) models > (method) list", OutputKind: OutputPageItem, Stdout: &out}
			opts.setDefaults()
			require.NoError(t, writeModelsList(iter, opts, test.width, 0))
			require.Contains(t, out.String(), test.want)
			require.NotContains(t, out.String(), test.hidden)
		})
	}
}

func TestModelsListViewerPreservesErrorsAndCancellation(t *testing.T) {
	upstream := errors.New("synthetic upstream failure")
	_, iter := staticModelIterator(t, `[]`, -1, upstream)
	var out bytes.Buffer
	opts := ShowJSONOpts{Operation: "(resource) models > (method) list", OutputKind: OutputPageItem, Stdout: &out}
	opts.setDefaults()
	require.ErrorIs(t, writeModelsList(iter, opts, 80, 0), upstream)
	require.Empty(t, out.String())
	source, iter := staticModelIterator(t, `[{"id":"model-a","object":"model","owned_by":"synthetic"}]`, -1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opts.Context, iter.context = ctx, ctx
	require.ErrorIs(t, writeModelsList(iter, opts, 80, 0), context.Canceled)
	require.Zero(t, source.Index())
	require.Empty(t, out.String())
	_, iter = staticModelIterator(t, `[{"id":"model-a","object":"model","owned_by":"synthetic"}]`, 1, upstream)
	opts.Context = context.Background()
	opts.Stdout = staticTableFailedWriter{}
	err := writeModelsList(iter, opts, 80, 0)
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.ErrorIs(t, err, upstream)
}

type staticTableFailedWriter struct{}

func (staticTableFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestModelsListViewerNeverConsumesCursorPager(t *testing.T) {
	// Its GetNextPage would need request configuration. Type rejection must occur
	// before even reading the first loaded item, regardless of operation metadata.
	source := pagination.NewCursorPageAutoPager(&pagination.CursorPage[openai.Model]{Data: []openai.Model{{ID: "unread"}}, HasMore: true}, nil)
	iter := &outputIterator[openai.Model]{source: source}
	handled, err := showModelsListViewer(source, iter, ShowJSONOpts{Operation: "(resource) models > (method) list", OutputKind: OutputPageItem})
	require.NoError(t, err)
	require.False(t, handled)
	require.Zero(t, source.Index())
}
