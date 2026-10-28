package batches

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func catalogFixture(t *testing.T, selection string, records ...string) *batchBrowserCatalog {
	t.Helper()
	store, err := newBatchRecordStore(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.close()) })
	require.NoError(t, store.readJSONL(strings.NewReader(strings.Join(records, "\n")), selection, "", selection == "input", false))
	catalog, err := newBatchBrowserCatalog(t.Context(), store, selection)
	require.NoError(t, err)
	return catalog
}

func TestBatchBrowserCatalogRecordIdentityAndPaging(t *testing.T) {
	catalog := catalogFixture(t, "input",
		`{"custom_id":"same","body":{"input":"First request"}}`,
		`{"custom_id":"same","body":{"input":"Second request"}}`,
		`{"body":{"input":"No ID"}}`,
		`{"custom_id":"","body":{"input":"Empty ID"}}`,
		`{"custom_id":123,"body":{"input":"Invalid ID"}}`)
	page, err := catalog.page(t.Context(), "", 0, 2)
	require.NoError(t, err)
	require.Equal(t, int64(5), page.Total)
	require.Equal(t, []batchBrowserRecordRow{{1, "same", "First request"}, {2, "same", "Second request"}}, page.Rows)
	page, err = catalog.page(t.Context(), "", math.MaxInt64, 3)
	require.NoError(t, err)
	require.Equal(t, int64(2), page.First)
	require.Equal(t, []batchBrowserRecordRow{{3, "(no custom_id)", "No ID"}, {4, "(empty custom_id)", "Empty ID"}, {5, "(invalid custom_id)", "Invalid ID"}}, page.Rows)
	page, err = catalog.page(t.Context(), "second request", 0, 2)
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Equal(t, int64(2), page.Rows[0].ID)
}

func TestBatchBrowserCatalogSearchesCompleteDecodedContent(t *testing.T) {
	longID := strings.Repeat("i", 2048) + "identify-me"
	input := strings.Repeat("a", 2*1024*1024) + " 素晴らしい ÉCLAIR aaaaab\nLast line"
	data, err := json.Marshal(map[string]any{"custom_id": longID, "body": map[string]any{"instructions": "Classify", "input": input, "max_output_tokens": 32}})
	require.NoError(t, err)
	catalog := catalogFixture(t, "input", string(data), `{"custom_id":"different","body":{"input":"Other"}}`)
	for _, query := range []string{"identify-me", "素晴らしい", "éclair", "aaaaab", "aaaaab\nLast line", "max_output_tokens", "32"} {
		t.Run(query, func(t *testing.T) {
			page, err := catalog.page(t.Context(), query, 0, 2)
			require.NoError(t, err)
			require.Equal(t, int64(1), page.Total)
			require.Equal(t, int64(1), page.Rows[0].ID)
			require.Less(t, len(page.Rows[0].Label), 600)
			require.Less(t, len(page.Rows[0].Preview), 600)
		})
	}
	page, err := catalog.page(t.Context(), "not present", 0, 5)
	require.NoError(t, err)
	require.Zero(t, page.Total)
	require.Empty(t, page.Rows)
}

func TestBatchBrowserCatalogEscapesControlsAndDecodesUnicode(t *testing.T) {
	catalog := catalogFixture(t, "input", `{"custom_id":"request\u001b[31m\u202e","body":{"messages":[{"role":"user","content":"Caf\u00e9\nHello\tworld\u001b[2J"}]}}`)
	page, err := catalog.page(t.Context(), "CAFÉ\nHELLO", 0, 5)
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.NotContains(t, page.Rows[0].Label, "\x1b")
	require.NotContains(t, page.Rows[0].Label, "\u202e")
	require.NotContains(t, page.Rows[0].Preview, "\x1b")
	require.NotContains(t, page.Rows[0].Preview, "\n")
	require.NotContains(t, page.Rows[0].Preview, "\t")
	require.Contains(t, page.Rows[0].Preview, "Café Hello world")
}

func TestBatchBrowserCatalogResultPreviews(t *testing.T) {
	catalog := catalogFixture(t, "output",
		`{"custom_id":"response","response":{"body":{"output":[{"content":[{"type":"output_text","text":"positive"}]}]}},"error":null}`,
		`{"custom_id":"chat","response":{"body":{"choices":[{"message":{"role":"assistant","content":"negative"}}]}}}`,
		`{"custom_id":"error","error":{"code":"bad_request","message":"Use a supported model"}}`,
		`{"custom_id":"embedding","response":{"body":{"data":[{"embedding":[0.1,0.2]}]}}}`)
	page, err := catalog.page(t.Context(), "", 0, 5)
	require.NoError(t, err)
	require.Equal(t, "positive", page.Rows[0].Preview)
	require.Equal(t, "negative", page.Rows[1].Preview)
	require.Equal(t, "Use a supported model", page.Rows[2].Preview)
	require.Equal(t, "Open request details", page.Rows[3].Preview)
}

func TestBatchBrowserCatalogCancellationAndConcurrentQueries(t *testing.T) {
	var records []string
	for i := range 50 {
		records = append(records, fmt.Sprintf(`{"custom_id":"record-%d","body":{"input":"value-%d"}}`, i, i))
	}
	catalog := catalogFixture(t, "input", records...)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := catalog.page(ctx, "value", 0, 5)
	require.ErrorIs(t, err, context.Canceled)
	_, err = newBatchBrowserCatalog(ctx, catalog.store, "input")
	require.ErrorIs(t, err, context.Canceled)
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			page, err := catalog.page(t.Context(), fmt.Sprintf("record-%d", i), 0, 5)
			require.NoError(t, err)
			require.Positive(t, page.Total)
			require.Equal(t, int64(i+1), page.Rows[0].ID)
		})
	}
	wg.Wait()
}

func TestBatchBrowserCatalogPreviewsUsePayloadPaths(t *testing.T) {
	catalog := catalogFixture(t, "input",
		`{"metadata":{"text":"misleading"},"custom_id":"input","body":{"metadata":{"text":"also misleading"},"input":[{"type":"input_text","text":"Actual input"}]}}`,
		`{"custom_id":"unknown","body":{"metadata":{"text":"not input"},"input":[{"type":"input_image","image_url":"https://example.invalid/synthetic"}]}}`)
	page, err := catalog.page(t.Context(), "", 0, 5)
	require.NoError(t, err)
	require.Equal(t, "Actual input", page.Rows[0].Preview)
	require.Equal(t, "Open request details", page.Rows[1].Preview)
	page, err = catalog.page(t.Context(), "also misleading", 0, 5)
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Equal(t, "Actual input", page.Rows[0].Preview)
}

func TestBatchBrowserCatalogTruncatesAtUTF8Boundary(t *testing.T) {
	input := strings.Repeat("a", batchBrowserCatalogHint-1) + "素晴らしい"
	data, err := json.Marshal(map[string]any{"custom_id": "unicode", "body": map[string]any{"input": input}})
	require.NoError(t, err)
	catalog := catalogFixture(t, "input", string(data))
	page, err := catalog.page(t.Context(), "素晴らしい", 0, 5)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("a", batchBrowserCatalogHint-1)+"…", page.Rows[0].Preview)
}

type batchCatalogCancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *batchCatalogCancelContext) Err() error {
	c.checks++
	if c.checks == 20 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestBatchBrowserCatalogCancelsDuringSearchAndRebuilds(t *testing.T) {
	data, err := json.Marshal(map[string]any{"custom_id": "large", "body": map[string]any{"input": strings.Repeat("x", 2*1024*1024) + "finish"}})
	require.NoError(t, err)
	catalog := catalogFixture(t, "input", string(data))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	searchContext := &batchCatalogCancelContext{Context: ctx, cancel: cancel}
	_, err = catalog.page(searchContext, "finish", 0, 5)
	require.ErrorIs(t, err, context.Canceled)
	require.GreaterOrEqual(t, searchContext.checks, 20)
	require.False(t, catalog.ready)
	page, err := catalog.page(t.Context(), "finish", 0, 5)
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	catalog.wait()
}
