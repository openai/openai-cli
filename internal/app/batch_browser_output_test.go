package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestBatchBrowserInputDetailShowsSettingsWithoutRepeatedFileMetadata(t *testing.T) {
	var out, diagnostics bytes.Buffer
	raw := `{"custom_id":"request-one","method":"POST","url":"/v1/responses","body":{"model":"synthetic","input":"Hello","instructions":"Classify","max_output_tokens":32,"reasoning":{"effort":"low"},"temperature":0}}`
	records := batchReadableOutput(batchcommands.RecordItem{Kind: "input_detail", WriteJSON: func(w io.Writer) error { _, err := io.WriteString(w, raw); return err }})
	records.Detail = true
	require.NoError(t, showReadableBatchRecords(t.Context(), records, &out, &diagnostics))
	for _, label := range []string{"Request: request-one", "Input: Hello", "Instructions: Classify", "Settings:", "Model: synthetic", "Max output tokens: 32", "Effort: low", "Temperature: 0"} {
		require.Contains(t, out.String(), label)
	}
	for _, label := range []string{"Batch:", "Source:", "File ID:"} {
		require.NotContains(t, out.String(), label)
	}
}

func TestBatchBrowserPrivateRecordOutputPreservesReadablePolicy(t *testing.T) {
	for _, tc := range []struct {
		command, format string
		readable        bool
	}{
		{"browse", "auto", true},
		{"browse", "text", true},
		{"results", "auto", true},
		{"results", "json", false},
		{"results", "text", true},
	} {
		t.Run(tc.command+"/"+tc.format, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			record := batchReadableRecord("positive")
			records := batchReadableOutput(batchReadableItem(record))
			records.WriteJSON = func(writer io.Writer) error {
				_, err := fmt.Fprintf(writer, `{"batch_id":"batch-demo","file":"output","file_id":"file-demo","data":[%s]}`, record)
				return err
			}
			root := &cli.Command{
				Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{&cli.StringFlag{Name: "format", Value: "auto"}},
				Commands: []*cli.Command{{Name: tc.command, Action: func(ctx context.Context, command *cli.Command) error {
					return showBatchRecords(ctx, command, records, &out, &diagnostics)
				}}},
			}
			args := []string{"openai"}
			if tc.format != "auto" {
				args = append(args, "--format", tc.format)
			}
			args = append(args, tc.command)
			require.NoError(t, root.Run(t.Context(), args))
			if tc.readable {
				require.Contains(t, out.String(), "Batch: batch-demo")
				require.Contains(t, out.String(), "Request: request-demo")
				require.Contains(t, out.String(), "Output: positive")
				require.False(t, gjson.ValidBytes(out.Bytes()), "readable formats must preserve their projection in a private sink")
			} else {
				require.True(t, gjson.ValidBytes(out.Bytes()), "explicit standalone JSON must preserve machine output")
				value := gjson.ParseBytes(out.Bytes())
				require.Equal(t, "batch-demo", value.Get("batch_id").Str)
				require.Equal(t, "request-demo", value.Get("data.0.custom_id").Str)
				require.Equal(t, "positive", value.Get("data.0.response.body.output.0.content.0.text").Str)
			}
		})
	}
}
