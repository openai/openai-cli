package batches

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func (w workflow) configureBatchListWatch(command *cli.Command) {
	command.Flags = append(command.Flags,
		&cli.BoolFlag{Name: "watch", Usage: "Refresh the selected Batch list as successive snapshots"},
		&cli.DurationFlag{Name: "poll-interval", Value: 10 * time.Second, Usage: "Delay between complete snapshots with --watch"},
		&cli.Int64Flag{Name: "watch-count", Usage: "Number of snapshots with --watch; zero watches until interrupted"},
	)
	previous := command.Action
	command.Action = func(ctx context.Context, cmd *cli.Command) error {
		if !cmd.Bool("watch") {
			if cmd.IsSet("poll-interval") || cmd.IsSet("watch-count") {
				return batchError("Use --watch with --poll-interval or --watch-count.")
			}
			return previous(ctx, cmd)
		}
		return w.watchBatchList(ctx, cmd)
	}
}

func (w workflow) watchBatchList(ctx context.Context, cmd *cli.Command) (err error) {
	root := cmd.Root()
	format := strings.ToLower(root.String("format"))
	if format != "" && format != "auto" && format != "text" && format != "jsonl" || root.IsSet("transform") || root.Bool("raw-output") {
		return batchError("Use --watch with readable output or --format jsonl, without --transform or --raw-output. Omit --watch for other formats.")
	}
	interval, count := cmd.Duration("poll-interval"), cmd.Int64("watch-count")
	if interval <= 0 || count < 0 {
		return batchError("--poll-interval must be positive, and --watch-count must be zero or positive.")
	}
	if cmd.Args().Len() != 0 {
		return batchError("Unexpected extra arguments. Use --after and --limit to choose the Batch list.")
	}
	maximum := int64(-1)
	if cmd.IsSet("max-items") {
		maximum = cmd.Int64("max-items")
	}
	if maximum < -1 {
		return batchError("--max-items must be -1, zero, or positive.")
	}
	ctx, stop := batchSignalContext(ctx)
	defer stop()
	defer func() {
		if ctx.Err() != nil {
			err = batchContextError(errors.Join(ctx.Err(), context.Cause(ctx), err))
		}
	}()
	options, err := w.options.RequestOptions(cmd, batchRequestInput{ctx: ctx, file: os.Stdin})
	if err != nil {
		return err
	}
	client := openai.NewClient(w.options.DefaultRequestOptions(cmd)...)
	for sequence := int64(1); count == 0 || sequence <= count; sequence++ {
		first := true
		items, fetchErr := batchFetchPages(ctx, cmd.String("after"), maximum, func(after string) ([]byte, error) {
			var raw []byte
			opts := append(options[:len(options):len(options)], option.WithResponseBodyInto(&raw))
			if !first {
				opts = append(opts, option.WithQuery("after", after))
			}
			first = false
			_, err := client.Batches.List(ctx, openai.BatchListParams{}, opts...)
			return raw, err
		}, nil)
		if fetchErr != nil && len(items) == 0 {
			return fetchErr
		}
		outputErr := w.showBatchSnapshot(ctx, cmd, items, sequence, fetchErr == nil, format == "jsonl")
		if err := errors.Join(fetchErr, outputErr); err != nil {
			return err
		}
		if count != 0 && sequence == count {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func (w workflow) showBatchSnapshot(ctx context.Context, cmd *cli.Command, items []gjson.Result, sequence int64, complete, jsonl bool) error {
	observed := time.Now().UTC().Format(time.RFC3339)
	if jsonl {
		prefix := `{"object":"batch.list_snapshot","sequence":` + strconv.FormatInt(sequence, 10) + `,"observed_at":` + strconv.Quote(observed) + `,"complete":` + strconv.FormatBool(complete) + `,"data":[`
		return writeBatchStdout(ctx, cmd.Root().Writer, func(out io.Writer) error {
			writer := clioutput.Writer{Context: ctx, Sink: out}
			if _, err := writer.WriteString(prefix); err != nil {
				return err
			}
			var compact bytes.Buffer
			for i, item := range items {
				if err := ctx.Err(); err != nil {
					return err
				}
				compact.Reset()
				if err := json.Compact(&compact, []byte(item.Raw)); err != nil {
					return err
				}
				if i != 0 {
					if _, err := writer.WriteString(","); err != nil {
						return err
					}
				}
				if _, err := writer.Write(compact.Bytes()); err != nil {
					return err
				}
			}
			_, err := writer.WriteString("]}\n")
			return err
		})
	}
	label := ""
	if !complete {
		label = " (partial)"
	}
	if err := writeBatchStdout(ctx, cmd.Root().Writer, func(out io.Writer) error {
		return readable.WriteText(out, fmt.Sprintf("Batch snapshot %d at %s%s", sequence, observed, label))
	}); err != nil {
		return err
	}
	if len(items) == 0 {
		if err := writeBatchStdout(ctx, cmd.Root().Writer, func(out io.Writer) error { return readable.WriteText(out, "No results.") }); err != nil {
			return err
		}
	}
	for _, item := range items {
		if err := w.batchShowJSON(ctx, cmd, item, "watch"); err != nil {
			return err
		}
	}
	if sequence == 1 && w.options.HumanTerminal(ctx, cmd.Root()) {
		return writeBatchStdout(ctx, os.Stderr, func(out io.Writer) error {
			return readable.WriteText(out, "Full snapshot data: --format jsonl.")
		})
	}
	return nil
}
