package custom

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRenderListNavigationPageCancelsProjection(t *testing.T) {
	large := strings.Repeat("synthetic-", 128*1024)
	for _, test := range []struct{ resource, input string }{
		{"files", `{"id":"file_example","object":"file","filename":"` + large + `","purpose":"batch","bytes":1,"status":"processed"}`},
		{"batches", `{"id":"batch_example","object":"batch","status":"completed","metadata":{"label":"` + large + `"},"request_counts":{"total":1,"completed":1,"failed":0}}`},
		{"admin.organization.projects", `{"id":"proj_example","object":"organization.project","name":"` + large + `","status":"active"}`},
	} {
		t.Run(test.resource, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			// The navigation boundary alone polls twice. Cancel during the
			// projection's field checks, without timers or scheduling races.
			controlled := &cancelListTableContext{Context: ctx, cancel: cancel, after: 8}
			item := gjson.Parse(test.input)
			content, err := renderListNavigationPage(ShowJSONOpts{
				Context: controlled, Operation: "(resource) " + test.resource + " > (method) list",
				OutputKind: OutputPageItem,
			}, []gjson.Result{item}, 80)
			require.ErrorIs(t, err, context.Canceled)
			require.Empty(t, content, "Cancellation must not return a partial table or trigger labeled fallback.")
			require.Equal(t, test.input, item.Raw)
		})
	}
}

type cancelListTableContext struct {
	context.Context
	cancel context.CancelFunc
	after  int32
	checks atomic.Int32
}

func (ctx *cancelListTableContext) Err() error {
	if ctx.checks.Add(1) == ctx.after {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestRenderListTablePage(t *testing.T) {
	items := []gjson.Result{gjson.Parse(`{"object":"batch","id":"batch_synthetic","status":"completed","created_at":1}`)}
	original := items[0].Raw
	content, supported, err := renderListTablePage(t.Context(), "(resource) batches > (method) list", items, 80)
	require.NoError(t, err)
	require.True(t, supported)
	require.Contains(t, content, "ID")
	require.Contains(t, content, "STATUS")
	require.Contains(t, content, "batch_synthetic")
	require.Equal(t, 1, strings.Count(content, resourceSummaryHint))
	require.Equal(t, original, items[0].Raw)
	// Rerendering reads only loaded values and cannot mutate their original JSON.
	_, supported, err = renderListTablePage(t.Context(), "(resource) batches > (method) list", items, 1)
	require.NoError(t, err)
	require.False(t, supported)
	require.Equal(t, original, items[0].Raw)
}

func TestRenderListTablePageEmptyAndUnsupported(t *testing.T) {
	content, supported, err := renderListTablePage(t.Context(), "(resource) files > (method) list", nil, 80)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, "No results.\n", content)
	for _, op := range []string{"", "(resource) files > (method) retrieve", "(resource) images > (method) models", "(resource) models > (method) list"} {
		content, supported, err = renderListTablePage(t.Context(), op, nil, 80)
		require.NoError(t, err)
		require.False(t, supported)
		require.Empty(t, content)
	}
}
