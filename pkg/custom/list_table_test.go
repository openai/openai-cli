package custom

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestRenderListNavigationPageHintPolicy(t *testing.T) {
	for _, resource := range []struct{ name, input, selected string }{
		{"files", `{"id":"file_exact","object":"file","filename":"input.jsonl","purpose":"batch","bytes":1024,"status":"processed","created_at":123}`, "input.jsonl"},
		{"batches", `{"id":"batch_exact","object":"batch","status":"completed","created_at":123,"metadata":{"label":"retained metadata"}}`, "completed"},
		{"admin.organization.projects", `{"id":"proj_exact","object":"organization.project","name":"Synthetic project","status":"active","created_at":123}`, "Synthetic project"},
	} {
		for _, layout := range []struct {
			name  string
			width int
			table bool
		}{{"table", 80, true}, {"fallback", 1, false}} {
			t.Run(resource.name+"/"+layout.name, func(t *testing.T) {
				items := []gjson.Result{gjson.Parse(resource.input)}
				original := append([]gjson.Result(nil), items...)
				opts := ShowJSONOpts{Context: t.Context(), Operation: "(resource) " + resource.name + " > (method) list", OutputKind: OutputPageItem}
				_, fits, err := renderListTablePage(t.Context(), opts.Operation, items, layout.width)
				require.NoError(t, err)
				require.Equal(t, layout.table, fits, "the fixture must exercise the selected renderer")
				ordinary, err := renderListNavigationPage(opts, items, layout.width)
				require.NoError(t, err)
				require.Contains(t, ordinary, items[0].Get("id").String())
				require.Contains(t, ordinary, resource.selected)
				// Projects retain their full record in the labeled fallback, so
				// that path has no omitted-field hint even without a policy.
				wantHint := layout.table || resource.name != "admin.organization.projects"
				require.Equal(t, wantHint, strings.HasSuffix(ordinary, resourceSummaryHint+"\n"))
				hintCount := 0
				if wantHint {
					hintCount = 1
				}
				require.Equal(t, hintCount, strings.Count(ordinary, resourceSummaryHint))
				data := strings.TrimSuffix(ordinary, resourceSummaryHint+"\n")
				for _, policy := range []struct {
					name  string
					flags []string
					hint  bool
				}{
					{"ordinary", nil, true},
					{"quiet", []string{"--quiet"}, false},
					{"quiet verbose", []string{"--quiet", "--verbose"}, false},
					{"machine errors", []string{"--format-error", "json"}, false},
					{"inherited machine errors", []string{"--format", "json"}, false},
					{"extracted errors", []string{"--transform-error", "message"}, false},
					{"explicit text override", []string{"--format", "json", "--format-error", "text"}, true},
				} {
					t.Run(policy.name, func(t *testing.T) {
						var content string
						root := receiptTestCommand(func(command *cli.Command) error {
							// The page is already selected. Parse actual flags for
							// its diagnostic policy without changing renderer routing.
							selected := opts
							selected.Context = outputPolicyContext(t.Context(), command)
							var err error
							content, err = renderListNavigationPage(selected, items, layout.width)
							return err
						})
						root.Metadata = map[string]any{"synthetic-marker": "unchanged"}
						require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, policy.flags...)))
						want := data
						if policy.hint && wantHint {
							want += resourceSummaryHint + "\n"
						}
						require.Equal(t, want, content, "policy may change only the optional hint")
						require.Equal(t, original, items, "rendering must preserve original records and metadata")
						require.Equal(t, map[string]any{"synthetic-marker": "unchanged"}, root.Metadata)
					})
				}
			})
		}
	}
}

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
