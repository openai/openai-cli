//go:build darwin || linux

package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
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

// PrepareKittyOutput binds the writer before a command reads input or waits for
// the API. Preparation errors are deferred until a preview actually uses it.
// Close reports helper failures only after use, preserving non-preview paths.
func PrepareKittyOutput(ctx context.Context, out io.Writer) (context.Context, func() error) {
	file, ok := out.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
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
// Only native cat writes graphics; its supervisor owns cancellation and reaping.
// Terminal modes, user descriptor flags and parent signal handlers are unchanged.
func writeKittyOutput(ctx context.Context, out io.Writer, write func(io.Writer) error) (err error) {
	file, ok := out.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return write(out)
	}
	binding, _ := ctx.Value(kittyOutputContextKey{}).(*kittyOutputBinding)
	if binding == nil {
		// Direct internal-library callers may have no command preparation phase.
		var closeOutput func() error
		ctx, closeOutput = PrepareKittyOutput(ctx, out)
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
	// Pipe acceptance does not identify the terminal's cut. Wait for cat before
	// NUL + ST + quiet final chunk. Keep the lease through bounded cleanup.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), kittyOutputCleanupTimeout)
	defer cancel()
	_, cleanupErr := session.write(cleanupCtx, func(destination io.Writer) error {
		_, err := io.WriteString(destination, "\x00\x1b\\\x1b_Gq=2,m=0;\x1b\\")
		return err
	})
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("reset interrupted image output: %w", cleanupErr)
	}
	return errors.Join(err, cleanupErr)
}

// Fixed system locations avoid PATH wrappers with different signal behavior.
func kittyCatPath() (string, error) {
	paths := []string{"/bin/cat"}
	if runtime.GOOS == "linux" {
		paths = append(paths, "/usr/bin/cat")
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return path, nil
		}
	}
	return "", errors.New("native image preview requires the system cat executable in /bin or /usr/bin")
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

// One-shot entry used by focused transport tests and direct helper callers.
func runKittyWriter(ctx context.Context, path string, out *os.File, write func(io.Writer) error) (written bool, err error) {
	session, err := startKittySession(ctx, path, out)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, session.Close()) }()
	return session.write(ctx, write)
}
