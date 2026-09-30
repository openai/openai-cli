//go:build darwin || linux

package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/charmbracelet/x/term"
)

const kittyOutputCleanupTimeout = 2 * time.Second

// Go's interrupted tty writes can resume after VINTR has flushed their header,
// exposing the remaining base64 as text. A native cat in the same foreground
// process group exits on SIGINT instead. It reads only our pipe, never stdin.
// No terminal modes, user-owned descriptor flags, or parent signal handlers change.
func writeKittyOutput(ctx context.Context, out io.Writer, write func(io.Writer) error) error {
	file, ok := out.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return write(out)
	}
	_, err := kittyCatPath()
	if err != nil {
		return err
	}
	path, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate native image output helper: %w", err)
	}
	written, err := runKittyWriter(ctx, path, file, write)
	if err == nil || !written {
		return err
	}
	// Pipe acceptance cannot tell us where the terminal cut a frame. Reap the
	// first writer before resetting: NUL prevents a dangling ESC from printing
	// a backslash, ST ends an APC, and the quiet final chunk ends its upload.
	// Cleanup has a separate bound so cancellation cannot wait on a paused tty.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), kittyOutputCleanupTimeout)
	defer cancel()
	_, cleanupErr := runKittyWriter(cleanupCtx, path, file, func(destination io.Writer) error {
		_, err := io.WriteString(destination, "\x00\x1b\\\x1b_Gq=2,m=0;\x1b\\")
		return err
	})
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("reset interrupted image output: %w", cleanupErr)
	}
	return errors.Join(err, cleanupErr)
}

// Use system locations rather than PATH, which can name a shell/Go wrapper with
// different signal behavior. Unsupported layouts fail before emitting graphics.
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

func runKittyWriter(ctx context.Context, path string, out *os.File, write func(io.Writer) error) (bool, error) {
	lifeRead, lifeWrite, err := os.Pipe()
	if err != nil {
		return false, fmt.Errorf("prepare image output lifeline: %w", err)
	}
	defer lifeRead.Close()
	defer lifeWrite.Close()
	command := exec.CommandContext(ctx, path, kittyOutputHelperArgument)
	command.Stdout = out
	command.ExtraFiles = []*os.File{lifeRead}
	// Let the supervisor kill and reap its native writer. Killing only the
	// supervisor could leave a blocked writer alive after the CLI returns.
	command.Cancel = func() error {
		_ = lifeWrite.Close()
		return nil
	}
	input, err := command.StdinPipe()
	if err != nil {
		return false, fmt.Errorf("prepare native image output: %w", err)
	}
	defer input.Close()
	if err := command.Start(); err != nil {
		return false, fmt.Errorf("start native image output: %w", err)
	}
	_ = lifeRead.Close()
	waited := false
	defer func() {
		if !waited {
			// Also release both children if the writer panics, without hiding
			// that panic or leaving a pipe feeding a blocked terminal writer.
			_ = lifeWrite.Close()
			_ = input.Close()
			_ = command.Wait()
		}
	}()
	writer := &kittyPipeWriter{Writer: input}
	writeErr := write(writer)
	closeErr := input.Close()
	if writeErr != nil {
		// Do not wait for queued bytes after an encoder/output failure. This
		// also releases a blocked tty writer when cancellation was observed.
		_ = lifeWrite.Close()
	}
	waitErr := command.Wait()
	waited = true
	if waitErr != nil {
		if _, exited := waitErr.(*exec.ExitError); exited {
			// A helper's ExitCode is not the CLI's exit status. Exposing it via
			// Unwrap would override command cancellation/error handling.
			waitErr = fmt.Errorf("write image to terminal: %v", waitErr)
		} else {
			waitErr = fmt.Errorf("write image to terminal: %w", waitErr)
		}
	}
	err = errors.Join(writeErr, closeErr, waitErr)
	if canceled := ctx.Err(); canceled != nil && !errors.Is(err, canceled) {
		err = errors.Join(err, canceled)
	}
	return writer.written, err
}
