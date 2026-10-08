package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

const batchOutputHelperArgument = "__batch-output"

const batchOutputCleanupGrace = 200 * time.Millisecond

type batchOwnedPipeClosed struct{ error }

func (e *batchOwnedPipeClosed) Unwrap() error { return e.error }

type batchPipeWriter struct {
	file    *os.File
	stopped *atomic.Bool
}

func (w batchPipeWriter) Write(data []byte) (int, error) {
	n, err := w.file.Write(data)
	if errors.Is(err, os.ErrClosed) && w.stopped.Load() {
		err = &batchOwnedPipeClosed{error: err}
	}
	return n, err
}

type batchTerminalWriter struct {
	io.Writer
	file *os.File
}

func (w batchTerminalWriter) Fd() uintptr                { return w.file.Fd() }
func (w batchTerminalWriter) Read(p []byte) (int, error) { return w.file.Read(p) }
func (w batchTerminalWriter) Close() error               { return nil } // The destination is borrowed.

type batchOutputInterrupted struct {
	error
	output *os.File
}

func (e *batchOutputInterrupted) Unwrap() error { return e.error }

// An exec.ExitError is also a CLI exit coder. Preserve the callback's status,
// never a deliberately killed private worker's status, while retaining causes.
type batchOutputResultError struct {
	primary error
	cause   error
}

func (e *batchOutputResultError) Error() string { return e.cause.Error() }
func (e *batchOutputResultError) Unwrap() error { return e.cause }
func (e *batchOutputResultError) ExitCode() int {
	var exit interface{ ExitCode() int }
	if errors.As(e.primary, &exit) {
		return exit.ExitCode()
	}
	return 1
}

// Do not attempt another diagnostic write to the destination whose delivery
// was interrupted. The exit status still reports cancellation or timeout.
func interruptedBatchDiagnostic(err error) bool {
	var interrupted *batchOutputInterrupted
	if !errors.As(err, &interrupted) {
		return false
	}
	if interrupted.output == os.Stderr {
		return true
	}
	out, outErr := interrupted.output.Stat()
	stderr, stderrErr := os.Stderr.Stat()
	return outErr == nil && stderrErr == nil && os.SameFile(out, stderr)
}

// RunBatchOutputHelper handles the private byte-copy process before CLI parsing.
// The helper owns no API client or saved files. Its lifeline stops blocked writes
// if the parent disappears, without changing the inherited stdout descriptor.
func RunBatchOutputHelper(args []string) (bool, error) {
	if len(args) < 2 || args[1] != batchOutputHelperArgument {
		return false, nil
	}
	if len(args) != 3 {
		return true, errors.New("invalid batch output helper invocation")
	}
	descriptor, err := strconv.ParseUint(args[2], 10, strconv.IntSize)
	if err != nil || descriptor < 3 {
		return true, errors.New("invalid batch output lifeline")
	}
	life := os.NewFile(uintptr(descriptor), "batch-output-lifeline")
	if life == nil {
		return true, errors.New("missing batch output lifeline")
	}
	defer life.Close()
	info, err := life.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return true, errors.New("batch output lifeline must be a pipe")
	}
	// Foreground Ctrl+C also reaches children. Let the parent's context and
	// lifeline decide shutdown, so an early child exit cannot race exit 130.
	signal.Ignore(batchOperationSignals...)
	go func() {
		var value [1]byte
		_, _ = life.Read(value[:])
		// Process exit is confined to this private helper. It releases a blocked
		// OS write even when the parent was killed without running cleanup.
		os.Exit(0)
	}()
	if _, err := os.Stderr.Write([]byte{'R'}); err != nil {
		return true, err
	}
	_, err = io.Copy(os.Stdout, os.Stdin)
	return true, err
}

// writeBatchStdout isolates pipe/socket/terminal writes. The parent keeps ownership
// of HTTP and file transactions, and kills/reaps its single writer on failure.
// An owned data pipe supplies backpressure without changing shared fd flags.
func writeBatchStdout(ctx context.Context, out io.Writer, write func(io.Writer) error) (err error) {
	return writeBatchStdoutWithCleanup(ctx, out, 0, write)
}

func writeBatchStdoutWithCleanup(ctx context.Context, out io.Writer, cleanupGrace time.Duration, write func(io.Writer) error) (err error) {
	file, ok := out.(*os.File)
	if !ok {
		return write(out)
	}
	defer func() {
		if ctx.Err() != nil {
			err = &batchOutputInterrupted{error: errors.Join(ctx.Err(), err), output: file}
		}
	}()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 && !isTerminal(file) {
		return write(out)
	}
	return streamBatchOutput(ctx, file, cleanupGrace, write)
}

func streamBatchOutput(ctx context.Context, out *os.File, cleanupGrace time.Duration, write func(io.Writer) error) (err error) {
	path, err := os.Executable()
	if err != nil {
		return batchDownloadFailure("Could not start the batch output writer.", err)
	}
	dataRead, dataWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer dataRead.Close()
	defer dataWrite.Close()
	lifeRead, lifeWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer lifeRead.Close()
	defer lifeWrite.Close()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	workerContext, stopWorker := context.WithCancel(context.WithoutCancel(ctx))
	terminal := isTerminal(out)
	parentStopped := make(chan struct{})
	stopParentCancellation := context.AfterFunc(ctx, func() {
		defer close(parentStopped)
		if terminal && cleanupGrace > 0 {
			// The operation stops immediately. Give its terminal renderer a bounded
			// chance to restore modes before killing a possibly blocked byte writer.
			timer := time.NewTimer(cleanupGrace)
			defer timer.Stop()
			select {
			case <-workerContext.Done():
				return
			case <-timer.C:
			}
		}
		stopWorker()
	})
	defer func() {
		stopWorker()
		if !stopParentCancellation() {
			<-parentStopped
		}
	}()
	command := exec.CommandContext(workerContext, path, batchOutputHelperArgument)
	var pipeStopped atomic.Bool
	closeWorkerPipes := func() {
		pipeStopped.Store(true)
		_ = dataWrite.Close()
		_ = readyRead.Close()
		_ = lifeWrite.Close()
	}
	command.Cancel = func() error {
		// Close the owned writer before stopping its reader. Blocked writes then
		// report owned-pipe closure rather than an unrelated downstream failure.
		closeWorkerPipes()
		return command.Process.Kill()
	}
	command.Stdin, command.Stdout, command.Stderr = dataRead, out, readyWrite
	command.Env = []string{"GOMAXPROCS=" + strconv.Itoa(runtime.GOMAXPROCS(0))}
	command.WaitDelay = 2 * time.Second
	if err := inheritBatchOutputLifeline(command, lifeRead); err != nil {
		return batchDownloadFailure("Could not prepare the batch output writer.", err)
	}
	if err := command.Start(); err != nil {
		return batchDownloadFailure("Could not start the batch output writer.", err)
	}
	waited := false
	wait := func() error {
		waited = true
		return command.Wait()
	}
	defer func() {
		if !waited {
			_ = dataWrite.Close()
			_ = lifeWrite.Close()
			stopWorker()
			_ = wait()
		}
	}()
	// The child owns these ends now. No write/read pump goroutine is needed.
	_ = dataRead.Close()
	_ = lifeRead.Close()
	_ = readyWrite.Close()
	stopped := make(chan struct{})
	stopCancellation := context.AfterFunc(workerContext, func() {
		defer close(stopped)
		closeWorkerPipes()
	})
	defer func() {
		if !stopCancellation() {
			<-stopped
		}
	}()
	// Bound startup, including a replaced executable that ignores the private
	// invocation. Never send response bytes before the ready acknowledgement.
	startup := time.AfterFunc(2*time.Second, stopWorker)
	var ready [1]byte
	_, readyErr := io.ReadFull(readyRead, ready[:])
	startup.Stop()
	_ = readyRead.Close()
	if readyErr != nil || ready[0] != 'R' || workerContext.Err() != nil || ctx.Err() != nil {
		stopWorker()
		waitErr := wait()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return batchDownloadFailure("The batch output writer did not become ready.", errors.Join(readyErr, waitErr))
	}
	var destination io.Writer = batchPipeWriter{file: dataWrite, stopped: &pipeStopped}
	if terminal {
		destination = batchTerminalWriter{Writer: destination, file: out}
	}
	writeErr := write(destination)
	closeErr := dataWrite.Close()
	if (writeErr != nil || closeErr != nil) && ctx.Err() == nil {
		stopWorker()
	}
	waitErr := wait()
	if waitErr == context.Canceled {
		// Only the private worker's control context was canceled here. Keep the
		// operation's own cause authoritative, including a deadline or SIGTERM.
		waitErr = nil
	}
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), writeErr, waitErr)
	}
	if writeErr != nil {
		return &batchOutputResultError{primary: writeErr, cause: errors.Join(writeErr, waitErr)}
	}
	if err := errors.Join(closeErr, waitErr); err != nil {
		return batchDownloadFailure("Could not write the batch output. Output may be incomplete.", err)
	}
	return nil
}

func interruptedBatchError(failure error) bool {
	var batch *batchWorkflowError
	return errors.As(failure, &batch) &&
		(errors.Is(failure, context.Canceled) || errors.Is(failure, context.DeadlineExceeded))
}

// Cancellation diagnostics get a separate bounded delivery window. A failed
// delivery must not trigger another unbounded write to the same error sink.
func withBatchErrorOutput(failure error, out io.Writer, render func(context.Context, io.Writer) error) error {
	if !interruptedBatchError(failure) {
		return render(context.Background(), out)
	}
	var batch *batchWorkflowError
	if errors.As(failure, &batch) && batch.message == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	var renderErr error
	err := writeBatchStdout(ctx, out, func(destination io.Writer) error {
		renderErr = render(ctx, destination)
		return renderErr
	})
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && errors.Is(err, context.DeadlineExceeded) &&
		batchDeliveryDeadlineOnly(renderErr) {
		return nil
	}
	return err
}

func batchDeliveryDeadlineOnly(err error) bool {
	if err == nil || err == context.DeadlineExceeded {
		return true
	}
	if _, owned := err.(*batchOwnedPipeClosed); owned {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !batchDeliveryDeadlineOnly(cause) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		cause := wrapped.Unwrap()
		return cause != nil && batchDeliveryDeadlineOnly(cause)
	}
	return false
}
