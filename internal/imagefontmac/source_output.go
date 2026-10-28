package imagefontmac

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

const (
	maxSourceFaceBytes = 64 << 20
	maxSourceFaces     = 4
	// Each face permits 64 MiB of tables plus base64 padding. Eight MiB covers
	// the JSON overhead for up to 65,535 four-byte table tags and variation axes,
	// bounded names and numeric metrics. These bounds also apply in source.js.
	maxSourceOutputBytes = maxSourceFaces * (((maxSourceFaceBytes+2)/3)*4 + (8 << 20))
	maxSourceErrorBytes  = 64 << 10
)

var errSourceOutputLimit = errors.New("installed-font bridge exceeded its output limit")

// Font-table export needs larger, explicitly bounded output than the other
// native operations. Overflow cancels the child and Run always waits for it.
func runSource(ctx context.Context, program string, args, environment []string) ([]byte, error) {
	return runSourceOutput(ctx, program, args, environment, maxSourceOutputBytes, maxSourceErrorBytes)
}

func runSourceOutput(ctx context.Context, program string, args, environment []string, outputLimit, errorLimit int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(childCtx, program, args...)
	command.Env = environment
	// Also bound waiting for inherited output pipes after cancellation or exit.
	command.WaitDelay = time.Second
	var output bytes.Buffer
	stdout := sourceOutputWriter{writer: &output, remaining: outputLimit, cancel: cancel}
	stderr := sourceOutputWriter{writer: io.Discard, remaining: errorLimit, cancel: cancel}
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, errSourceOutputLimit
	}
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

type sourceOutputWriter struct {
	writer    io.Writer
	remaining int
	cancel    context.CancelFunc
	exceeded  bool
}

func (w *sourceOutputWriter) Write(data []byte) (int, error) {
	if len(data) > w.remaining {
		w.exceeded = true
		w.cancel()
		return 0, errSourceOutputLimit
	}
	n, err := w.writer.Write(data)
	w.remaining -= n
	return n, err
}
