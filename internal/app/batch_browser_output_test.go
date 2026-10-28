package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestBatchBrowserPrivateRecordOutputPreservesReadablePolicy(t *testing.T) {
	for _, tc := range []struct {
		command, format string
		readable        bool
	}{
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
