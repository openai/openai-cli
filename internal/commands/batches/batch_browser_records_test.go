package batches

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestBatchBrowserRecordNavigationReusesSnapshotAndPreservesSelection(t *testing.T) {
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets++
		require.Equal(t, "/files/file_input/content", r.URL.Path)
		_, _ = io.WriteString(w, "{\"custom_id\":\"duplicate\",\"body\":{\"input\":\"first\"}}\n{\"custom_id\":\"duplicate\",\"body\":{\"input\":\"second\",\"max_output_tokens\":42}}\n")
	}))
	defer server.Close()
	root := batchDownloadTestCommand(t, server.URL, io.Discard)
	w := workflow{options: testOptions()}
	command := w.newBatchBrowseCommand()
	var list, readable, raw *batchBrowserScreen
	step := 0
	command.Action = func(ctx context.Context, cmd *cli.Command) error {
		client := openai.NewClient(w.options.DefaultRequestOptions(cmd)...)
		return w.browseBatchRecords(ctx, cmd, client, nil, gjson.Parse(`{"id":"batch_one","input_file_id":"file_input"}`), "input", func(ctx context.Context, screen *batchBrowserScreen) error {
			var content bytes.Buffer
			if screen.document != nil {
				require.NoError(t, screen.document.writeTo(ctx, &content))
			}
			switch step {
			case 0:
				list = screen
				require.Equal(t, "requests", screen.kind)
				screen.input.SetValue("second")
				screen.requestState.selected = 1
				screen.chosen = "record:2"
			case 1:
				readable = screen
				require.Equal(t, "batch_one / Record 2", screen.subtitle)
				require.Contains(t, content.String(), "second")
				require.NotContains(t, content.String(), "first")
				screen.chosen = "toggle_raw"
			case 2:
				raw = screen
				require.True(t, screen.documentRaw)
				require.True(t, gjson.Valid(content.String()))
				require.Equal(t, int64(42), gjson.Get(content.String(), "body.max_output_tokens").Int())
				screen.chosen = "toggle_raw"
			case 3:
				require.Same(t, readable, screen)
				screen.chosen = "back"
			case 4:
				require.Same(t, list, screen)
				require.Equal(t, "second", screen.input.Value())
				require.Equal(t, int64(1), screen.requestState.selected)
				screen.chosen = "all"
			case 5:
				require.Contains(t, content.String(), "first")
				require.Contains(t, content.String(), "second")
				screen.chosen = "back"
			case 6:
				require.Same(t, list, screen)
				screen.chosen = "back"
			default:
				t.Fatal("unexpected additional screen")
			}
			step++
			return nil
		}, func(context.Context) error { t.Fatal("viewing must not print"); return nil })
	}
	root.Commands[0].Commands = []*cli.Command{command}
	require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "browse"}))
	require.Equal(t, 7, step)
	require.Equal(t, 1, gets)
	for _, screen := range []*batchBrowserScreen{readable, raw} {
		require.ErrorIs(t, screen.document.writeTo(t.Context(), io.Discard), io.ErrClosedPipe)
	}
}
