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
	"time"
)

const batchOutputHelperArgument = "__batch-output"

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
	descriptor, err := strconv.ParseUint(args[2], 10, 64)
	if err != nil || descriptor < 3 || descriptor > uint64(^uintptr(0)) {
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
	signal.Ignore(os.Interrupt)
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
	return streamBatchOutput(ctx, file, write)
}

func streamBatchOutput(ctx context.Context, out *os.File, write func(io.Writer) error) (err error) {
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
	workerContext, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	command := exec.CommandContext(workerContext, path, batchOutputHelperArgument)
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
		_ = dataWrite.Close()
		_ = readyRead.Close()
		_ = lifeWrite.Close()
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
	if readyErr != nil || ready[0] != 'R' || workerContext.Err() != nil {
		stopWorker()
		waitErr := wait()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return batchDownloadFailure("The batch output writer did not become ready.", errors.Join(readyErr, waitErr))
	}
	var destination io.Writer = dataWrite
	if isTerminal(out) {
		destination = batchTerminalWriter{Writer: dataWrite, file: out}
	}
	writeErr := write(destination)
	closeErr := dataWrite.Close()
	if writeErr != nil || closeErr != nil {
		stopWorker()
	}
	waitErr := wait()
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
