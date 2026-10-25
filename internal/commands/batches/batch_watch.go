package batches

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
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
		err := w.collectBatchSnapshot(ctx, cmd, sequence, maximum, format == "jsonl", func(after string) ([]byte, error) {
			var raw []byte
			opts := append(options[:len(options):len(options)], option.WithResponseBodyInto(&raw))
			if !first {
				opts = append(opts, option.WithQuery("after", after))
			}
			first = false
			_, err := client.Batches.List(ctx, openai.BatchListParams{}, opts...)
			return raw, err
		})
		if err != nil {
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

func (w workflow) collectBatchSnapshot(ctx context.Context, cmd *cli.Command, sequence, maximum int64, jsonl bool, fetch func(string) ([]byte, error)) (err error) {
	store, err := newBatchRecordStore(ctx)
	if err != nil {
		return batchWatchStorageError(err)
	}
	// Release every snapshot before the poll delay or the next request.
	defer func() { err = errors.Join(err, batchWatchStorageError(store.close())) }()
	snapshot := batchWatchSnapshot{store: store}
	fetchErr := snapshot.fetch(cmd.String("after"), maximum, fetch)
	if fetchErr != nil && store.recordCount == 0 {
		return fetchErr
	}
	return errors.Join(fetchErr, w.showBatchSnapshot(ctx, cmd, &snapshot, sequence, fetchErr == nil, jsonl))
}

func (w workflow) showBatchSnapshot(ctx context.Context, cmd *cli.Command, snapshot *batchWatchSnapshot, sequence int64, complete, jsonl bool) error {
	observed := time.Now().UTC().Format(time.RFC3339)
	if jsonl {
		prefix := `{"object":"batch.list_snapshot","sequence":` + strconv.FormatInt(sequence, 10) + `,"observed_at":` + strconv.Quote(observed) + `,"complete":` + strconv.FormatBool(complete) + `,"data":[`
		return writeBatchStdout(ctx, cmd.Root().Writer, func(out io.Writer) error {
			writer := clioutput.Writer{Context: ctx, Sink: out}
			if _, err := writer.WriteString(prefix); err != nil {
				return err
			}
			for id := int64(1); id <= snapshot.store.recordCount; id++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				if id > 1 {
					if _, err := writer.WriteString(","); err != nil {
						return err
					}
				}
				if err := snapshot.writeRecord(writer, id); err != nil {
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
	var renderErr error
	outputErr := writeBatchStdoutWithCleanup(ctx, cmd.Root().Writer, batchOutputCleanupGrace, func(out io.Writer) error {
		heading := fmt.Sprintf("Batch snapshot %d at %s%s", sequence, observed, label)
		renderErr = w.writeReadableBatchSnapshot(ctx, cmd, snapshot, heading, out)
		// Drain previously rendered bytes before reporting a later failure.
		// The caller's context still stops an undrained output pipe.
		return nil
	})
	if err := errors.Join(renderErr, outputErr); err != nil {
		return err
	}
	if sequence == 1 && w.options.HumanTerminal(ctx, cmd.Root()) {
		return writeBatchStdout(ctx, os.Stderr, func(out io.Writer) error {
			return readable.WriteText(out, "Full snapshot data: --format jsonl.")
		})
	}
	return nil
}

func (w workflow) writeReadableBatchSnapshot(ctx context.Context, cmd *cli.Command, snapshot *batchWatchSnapshot, heading string, out io.Writer) error {
	if err := readable.WriteText(out, heading); err != nil {
		return err
	}
	if snapshot.store.recordCount == 0 {
		return readable.WriteText(out, "No results.")
	}
	diagnostics := terminal.BorrowedWriter{Writer: batchDiagnosticWriter{ctx: ctx, file: os.Stderr}, File: os.Stderr}
	for id := int64(1); id <= snapshot.store.recordCount; id++ {
		item, err := snapshot.item(id)
		if err != nil {
			return err
		}
		if err := w.options.ShowResult(ctx, cmd, item, "watch", out, diagnostics); err != nil {
			return err
		}
	}
	return nil
}
