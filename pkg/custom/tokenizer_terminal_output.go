package custom

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/urfave/cli/v3"
)

const tokenizerTerminalFrameBytes = 32 * 1024

var tokenizerTerminalReady = [8]byte{'T', 'O', 'K', 'O', 'U', 'T', 1, 0}

// tokenizerTerminalOutput isolates terminal writes in one disposable process.
// The original terminal remains borrowed for geometry and mode restoration.
type tokenizerTerminalOutput struct {
	executable string
	output     *os.File
	ctx        context.Context
	cancel     context.CancelFunc
	gate       chan struct{}
	worker     *tokenizerTerminalWorker // Owned while holding gate.
	closeOnce  sync.Once
	closeErr   error
}

type tokenizerTerminalWorker struct {
	command   *exec.Cmd
	input     *os.File
	ack       *os.File
	sequence  uint32
	closeOnce sync.Once
	closeErr  error
}

func newTokenizerTerminalOutput(ctx context.Context, executable string, output *os.File) (*tokenizerTerminalOutput, error) {
	if output == nil {
		return nil, errors.New("terminal output is unavailable")
	}
	life, cancel := context.WithCancel(context.Background())
	w := &tokenizerTerminalOutput{executable: executable, output: output, ctx: life, cancel: cancel, gate: make(chan struct{}, 1)}
	w.gate <- struct{}{}
	// Establish the private protocol before the caller changes terminal modes.
	if err := w.prepare(ctx); err != nil {
		return nil, errors.Join(err, w.Close())
	}
	return w, nil
}

func (w *tokenizerTerminalOutput) operationContext(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	stop := context.AfterFunc(w.ctx, func() { defer close(stopped); cancel() })
	return ctx, func() {
		if !stop() {
			<-stopped
		}
		cancel()
	}
}

func (w *tokenizerTerminalOutput) prepare(ctx context.Context) error {
	ctx, finish := w.operationContext(ctx)
	defer finish()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.gate:
	}
	defer func() { w.gate <- struct{}{} }()
	return w.ensureWorker(ctx)
}

func (w *tokenizerTerminalOutput) ensureWorker(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.worker != nil {
		return nil
	}
	startup, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	worker, err := startTokenizerTerminalWorker(startup, w.executable, w.output)
	if err != nil {
		return err
	}
	w.worker = worker
	return nil
}

// WriteContext reports only acknowledged bytes. A failed frame can already
// have reached the terminal partly, so callers must treat output as incomplete.
func (w *tokenizerTerminalOutput) WriteContext(ctx context.Context, data []byte) (written int, err error) {
	ctx, finish := w.operationContext(ctx)
	defer finish()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-w.gate:
	}
	defer func() { w.gate <- struct{}{} }()
	if err := w.ensureWorker(ctx); err != nil {
		return 0, err
	}
	worker := w.worker
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(stopped); _ = worker.stop() })
	defer func() {
		if !stop() {
			<-stopped
			w.worker = nil
			err = errors.Join(err, ctx.Err(), worker.stop())
		}
	}()
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			w.worker = nil
			return written, errors.Join(err, worker.stop())
		}
		size := min(len(data), tokenizerTerminalFrameBytes)
		worker.sequence++
		n, err := worker.writeFrame(worker.sequence, data[:size])
		written += n
		if err != nil || ctx.Err() != nil {
			w.worker = nil
			return written, errors.Join(err, ctx.Err(), worker.stop())
		}
		data = data[size:]
	}
	return written, nil
}

func (w *tokenizerTerminalOutput) Close() error {
	w.closeOnce.Do(func() {
		// Cancel before acquiring gate: an in-flight pipe write or ack read
		// must first stop its child and release the serialized writer.
		w.cancel()
		<-w.gate
		if w.worker != nil {
			w.closeErr = w.worker.stop()
			w.worker = nil
		}
		w.gate <- struct{}{}
	})
	return w.closeErr
}

func tokenizerTerminalEnvironment() []string {
	environment := []string{"GOMAXPROCS=" + strconv.Itoa(runtime.GOMAXPROCS(0))}
	if runtime.GOOS == "windows" {
		if root := os.Getenv("SystemRoot"); root != "" {
			environment = append(environment, "SystemRoot="+root)
		}
	}
	return environment
}

func startTokenizerTerminalWorker(ctx context.Context, executable string, output *os.File) (result *tokenizerTerminalWorker, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer inputRead.Close()
	ackRead, ackWrite, err := os.Pipe()
	if err != nil {
		inputWrite.Close()
		return nil, err
	}
	defer ackWrite.Close()
	command := exec.Command(executable, "tokenizer", "__output")
	command.Stdin, command.Stdout, command.Stderr = inputRead, output, ackWrite
	command.Env = tokenizerTerminalEnvironment()
	if err := command.Start(); err != nil {
		inputWrite.Close()
		ackRead.Close()
		return nil, err
	}
	_ = inputRead.Close()
	_ = ackWrite.Close()
	worker := &tokenizerTerminalWorker{command: command, input: inputWrite, ack: ackRead}
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(stopped); _ = worker.stop() })
	defer func() {
		if !stop() {
			<-stopped
			result = nil
			err = errors.Join(err, ctx.Err(), worker.stop())
		}
	}()
	var ready [8]byte
	_, err = io.ReadFull(ackRead, ready[:])
	if err != nil || ready != tokenizerTerminalReady || ctx.Err() != nil {
		return nil, errors.Join(errors.New("terminal output helper did not become ready"), err, ctx.Err(), worker.stop())
	}
	return worker, nil
}

func (w *tokenizerTerminalWorker) stop() error {
	w.closeOnce.Do(func() {
		// These owned pipe ends also release a parent blocked before receiving
		// acknowledgement. Reap the child before any replacement can start.
		_ = w.input.Close()
		_ = w.ack.Close()
		_ = w.command.Process.Kill()
		err := w.command.Wait()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			w.closeErr = err
		}
	})
	return w.closeErr
}

func (w *tokenizerTerminalWorker) writeFrame(sequence uint32, data []byte) (int, error) {
	var header [8]byte
	binary.BigEndian.PutUint32(header[:4], sequence)
	binary.BigEndian.PutUint32(header[4:], uint32(len(data)))
	if err := writeTokenizerTerminalBytes(w.input, header[:]); err != nil {
		return 0, err
	}
	if err := writeTokenizerTerminalBytes(w.input, data); err != nil {
		return 0, err
	}
	var ack [9]byte
	if _, err := io.ReadFull(w.ack, ack[:]); err != nil {
		return 0, err
	}
	n := binary.BigEndian.Uint32(ack[4:8])
	if binary.BigEndian.Uint32(ack[:4]) != sequence || n > uint32(len(data)) || ack[8] > 1 {
		return 0, errors.New("invalid terminal output acknowledgement")
	}
	if ack[8] != 0 || int(n) != len(data) {
		return int(n), errors.New("terminal output was incomplete")
	}
	return int(n), nil
}

func writeTokenizerTerminalBytes(writer io.Writer, data []byte) error {
	n, err := writer.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}

func tokenizerTerminalOutputCommand() *cli.Command {
	return &cli.Command{
		Name: "__output", Hidden: true, HideHelpCommand: true,
		Action: func(ctx context.Context, command *cli.Command) error {
			input, ok := command.Root().Reader.(io.ReadCloser)
			if !ok || command.Args().Present() {
				return errors.New("invalid terminal output helper invocation")
			}
			// Root.ErrWriter buffers human-facing diagnostics. This private
			// native protocol must acknowledge through the actual stderr pipe.
			return serveTokenizerTerminalOutput(ctx, input, command.Root().Writer, os.Stderr)
		},
	}
}

type tokenizerTerminalFrame struct {
	sequence uint32
	data     []byte
}

// This action runs only in the disposable native helper. Returning terminates
// its process, including a synchronous terminal write or native stdin read.
func serveTokenizerTerminalOutput(ctx context.Context, input io.ReadCloser, output, ack io.Writer) error {
	defer input.Close()
	if err := writeTokenizerTerminalBytes(ack, tokenizerTerminalReady[:]); err != nil {
		return err
	}
	frames := make(chan tokenizerTerminalFrame, 1)
	readerDone := make(chan error, 1)
	go func() { readerDone <- readTokenizerTerminalFrames(input, frames) }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readerDone:
			return err
		case frame := <-frames:
			done := make(chan error, 1)
			go func() { done <- writeTokenizerTerminalFrame(output, ack, frame) }()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-readerDone:
				return err
			case err := <-done:
				if err != nil {
					return err
				}
			}
		}
	}
}

func readTokenizerTerminalFrames(input io.Reader, frames chan<- tokenizerTerminalFrame) error {
	var sequence uint32
	for {
		var header [8]byte
		_, err := io.ReadFull(input, header[:])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		sequence++
		size := binary.BigEndian.Uint32(header[4:])
		if binary.BigEndian.Uint32(header[:4]) != sequence || size == 0 || size > tokenizerTerminalFrameBytes {
			return errors.New("invalid terminal output frame")
		}
		data := make([]byte, int(size))
		if _, err := io.ReadFull(input, data); err != nil {
			return err
		}
		select {
		case frames <- tokenizerTerminalFrame{sequence, data}:
		default:
			// Never block the lifeline reader behind unacknowledged writes.
			return errors.New("unacknowledged terminal output frames")
		}
	}
}

func writeTokenizerTerminalFrame(output, ack io.Writer, frame tokenizerTerminalFrame) error {
	n, err := output.Write(frame.data)
	if n < 0 || n > len(frame.data) {
		return errors.New("invalid terminal output write count")
	}
	if err == nil && n != len(frame.data) {
		err = io.ErrShortWrite
	}
	var result [9]byte
	binary.BigEndian.PutUint32(result[:4], frame.sequence)
	binary.BigEndian.PutUint32(result[4:8], uint32(n))
	if err != nil {
		result[8] = 1
	}
	return errors.Join(err, writeTokenizerTerminalBytes(ack, result[:]))
}
