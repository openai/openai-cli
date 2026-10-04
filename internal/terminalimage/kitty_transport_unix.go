//go:build darwin || linux

package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/x/term"
)

const kittyOutputCleanupTimeout = 2 * time.Second

type kittyOutputContextKey struct{}
type kittyOutputBinding struct {
	output  *os.File
	session *kittyOutputSession
	err     error
}

// PrepareNativeImageOutput binds the writer before a command reads input or waits for
// the API. Preparation errors are deferred until a preview actually uses it.
// Close reports helper failures only after use, preserving non-preview paths.
// The existing Kitty worker also carries VS Code's IIP bytes without interpreting them.
func PrepareNativeImageOutput(ctx context.Context, out io.Writer) (context.Context, func() error) {
	file, ok := out.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return ctx, func() error { return nil }
	}
	// A picker owns this binding across its requests. Nested saving workflows
	// reuse it without taking ownership of closure or reopening the executable.
	// writeKittyOutput still rejects a different output descriptor.
	if binding, _ := ctx.Value(kittyOutputContextKey{}).(*kittyOutputBinding); binding != nil {
		return ctx, func() error { return nil }
	}
	binding := &kittyOutputBinding{output: file}
	path, err := os.Executable()
	if err == nil {
		binding.session, err = startKittySession(ctx, path, file)
	}
	binding.err = err
	return context.WithValue(ctx, kittyOutputContextKey{}, binding), func() error {
		if binding.session == nil {
			return nil
		}
		err := binding.session.Close()
		if !binding.session.used.Load() {
			return nil
		}
		return err
	}
}

// Go's interrupted tty writes can resume after VINTR flushes their header.
// Only a directly owned worker with kernel-default SIGINT writes graphics.
// Terminal modes, user descriptor flags and parent signal handlers are unchanged.
func writeKittyOutput(ctx context.Context, out io.Writer, write func(io.Writer) error) (err error) {
	return writeNativeImageOutput(ctx, out, write, "\x00\x1b\\\x1b_Gq=2,m=0;\x1b\\")
}

func writeITermOutput(ctx context.Context, out io.Writer, write func(io.Writer) error) error {
	return writeNativeImageOutput(ctx, out, write, "\x00\x1b\\")
}

func writeNativeImageOutput(ctx context.Context, out io.Writer, write func(io.Writer) error, reset string) (err error) {
	file, ok := out.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return write(out)
	}
	binding, _ := ctx.Value(kittyOutputContextKey{}).(*kittyOutputBinding)
	if binding == nil {
		// Direct internal-library callers may have no command preparation phase.
		var closeOutput func() error
		ctx, closeOutput = PrepareNativeImageOutput(ctx, out)
		defer func() { err = errors.Join(err, closeOutput()) }()
		binding, _ = ctx.Value(kittyOutputContextKey{}).(*kittyOutputBinding)
	}
	if binding == nil || binding.output != file {
		return errors.New("native image output changed after preparation")
	}
	if binding.err != nil {
		return fmt.Errorf("prepare native image output: %w", binding.err)
	}
	session := binding.session
	session.used.Store(true)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-session.gate:
	}
	defer func() { session.gate <- struct{}{} }()
	written, err := session.write(ctx, write)
	if err == nil || !written {
		return err
	}
	// Pipe acceptance does not identify the terminal's cut. Reap failed workers before
	// the protocol-specific reset. Keep the lease through bounded cleanup.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), kittyOutputCleanupTimeout)
	defer cancel()
	_, cleanupErr := session.write(cleanupCtx, func(destination io.Writer) error {
		_, err := io.WriteString(destination, reset)
		return err
	})
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("reset interrupted image output: %w", cleanupErr)
	}
	return errors.Join(err, cleanupErr)
}

type kittyPipeWriter struct {
	io.Writer
	written bool
}

func (w *kittyPipeWriter) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	w.written = w.written || n > 0
	return n, err
}
