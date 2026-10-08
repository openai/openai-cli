package custom

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/tokenizer"
	"github.com/urfave/cli/v3"
)

var tokenizerPreviewMagic = [8]byte{'O', 'A', 'I', 'T', 'O', 'K', 1, 0}

// The preview keeps only IDs and byte endpoints. Text remains in its immutable
// input snapshot; even one token per byte needs at most 8 MiB of token storage.
type tokenizerPreviewToken struct {
	ID      uint32
	EndByte uint32
}

type tokenizerPreviewResult struct {
	Revision uint64
	Tokens   []tokenizerPreviewToken
	Err      error
}

type tokenizerPreviewRequest struct {
	revision   uint64
	generation uint64
	text       string
	encoding   string
}

// tokenizerPreviewWorker owns one child and one pending replacement. The child
// isolates the synchronous encoder so editing and quitting can stop its work.
type tokenizerPreviewWorker struct {
	ctx        context.Context
	cancel     context.CancelFunc
	executable string
	mu         sync.Mutex
	pending    *tokenizerPreviewRequest
	active     context.CancelFunc
	revision   uint64
	generation uint64
	closed     bool
	wake       chan struct{}
	results    chan tokenizerPreviewResult
	done       chan struct{}
}

func newTokenizerPreviewWorker(ctx context.Context, executable string) *tokenizerPreviewWorker {
	ctx, cancel := context.WithCancel(ctx)
	w := &tokenizerPreviewWorker{
		ctx: ctx, cancel: cancel, executable: executable,
		wake: make(chan struct{}, 1), results: make(chan tokenizerPreviewResult, 1), done: make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *tokenizerPreviewWorker) Replace(revision uint64, text, encoding string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || revision < w.revision {
		return
	}
	w.revision = revision
	w.generation++
	w.pending = &tokenizerPreviewRequest{revision, w.generation, text, encoding}
	if w.active != nil {
		w.active()
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *tokenizerPreviewWorker) Results() <-chan tokenizerPreviewResult { return w.results }

// Cancel immediately retires the current preview without scheduling another.
// The manager reaps its child asynchronously; Close waits for that cleanup.
func (w *tokenizerPreviewWorker) Cancel() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.generation++
	w.pending = nil
	if w.active != nil {
		w.active()
	}
	select {
	case <-w.results:
	default:
	}
}

func (w *tokenizerPreviewWorker) Close() error {
	w.mu.Lock()
	w.closed = true
	w.pending = nil
	w.cancel()
	w.mu.Unlock()
	<-w.done
	return nil
}

func (w *tokenizerPreviewWorker) run() {
	defer close(w.done)
	defer close(w.results)
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.wake:
		}
		for {
			w.mu.Lock()
			request := w.pending
			w.pending = nil
			if request == nil || w.closed || w.ctx.Err() != nil {
				w.mu.Unlock()
				break
			}
			ctx, cancel := context.WithCancel(w.ctx)
			w.active = cancel
			w.mu.Unlock()

			tokens, err := runTokenizerPreview(ctx, w.executable, request.text, request.encoding)
			cancel()
			w.mu.Lock()
			w.active = nil
			if !w.closed && w.ctx.Err() == nil && w.pending == nil && request.generation == w.generation {
				// Only the latest completed revision needs delivery. The UI must
				// also compare revisions before displaying an asynchronous result.
				select {
				case <-w.results:
				default:
				}
				w.results <- tokenizerPreviewResult{request.revision, tokens, err}
			}
			w.mu.Unlock()
		}
	}
}

func tokenizerPreviewCommand() *cli.Command {
	return &cli.Command{
		Name: "__preview", Hidden: true, HideHelpCommand: true,
		Action: func(ctx context.Context, command *cli.Command) error {
			input, ok := command.Root().Reader.(io.ReadCloser)
			if !ok || command.Args().Present() {
				return tokenizerPreviewFailure(nil)
			}
			if file, ok := input.(*os.File); ok && isTerminal(file) {
				return tokenizerPreviewFailure(nil)
			}
			if file, ok := command.Root().Writer.(*os.File); ok && isTerminal(file) {
				return tokenizerPreviewFailure(nil)
			}
			if err := serveTokenizerPreview(ctx, input, command.Root().Writer); err != nil {
				return tokenizerPreviewFailure(err)
			}
			return nil
		},
	}
}

func tokenizerPreviewFailure(cause error) error {
	return &localUtilityError{
		message: "Could not update the token preview. Edit the text to retry, or use tokenizer inspect for plain output.",
		cause:   cause,
	}
}

func tokenizerPreviewEncoding(encoding string) (byte, error) {
	switch encoding {
	case "o200k_base":
		return 0, nil
	case "cl100k_base":
		return 1, nil
	default:
		return 0, errors.New("invalid preview encoding")
	}
}

// These are the largest ordinary IDs in the pinned tokenizer v0.7.0
// codec/o200k_base_vocab.go and codec/cl100k_base_vocab.go files. The preview
// never encodes special markers as special IDs. Check this with library updates.
func tokenizerPreviewMaximumID(encoding byte) uint32 {
	if encoding == 1 {
		return 100255
	}
	return 199997
}

func runTokenizerPreview(ctx context.Context, executable, text, encoding string) (tokens []tokenizerPreviewToken, err error) {
	parentContext := ctx
	if _, err := validateTokenizerInput(text); err != nil {
		return nil, err
	}
	encodingID, err := tokenizerPreviewEncoding(encoding)
	if err != nil {
		return nil, tokenizerPreviewFailure(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "tokenizer", "__preview")
	// The child never starts another process. Bound cleanup of inherited pipes
	// too, so a replaced executable cannot keep the editor waiting indefinitely.
	command.WaitDelay = 250 * time.Millisecond
	command.Stderr = io.Discard
	inputReader, input, err := os.Pipe()
	if err != nil {
		return nil, tokenizerPreviewFailure(err)
	}
	defer inputReader.Close()
	defer input.Close()
	output, outputWriter, err := os.Pipe()
	if err != nil {
		return nil, tokenizerPreviewFailure(err)
	}
	defer output.Close()
	defer outputWriter.Close()
	command.Stdin = inputReader
	command.Stdout = outputWriter
	if err := command.Start(); err != nil {
		return nil, tokenizerPreviewFailure(err)
	}
	_ = inputReader.Close()
	_ = outputWriter.Close()
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(stopped)
		_ = input.Close()
		_ = output.Close()
	})
	defer func() {
		if !stop() {
			<-stopped
		}
	}()
	// Keep stdin open after the frame. Its EOF tells the child the editor died,
	// including abrupt parent termination during the synchronous BPE operation.
	if err = writeTokenizerPreviewInput(input, text, encodingID); err == nil {
		tokens, err = readTokenizerPreviewOutput(output, len(text), encodingID)
	}
	if err != nil {
		cancel()
	}
	waitErr := command.Wait()
	if parentContext.Err() != nil {
		return nil, tokenizerPreviewFailure(errors.Join(err, waitErr, parentContext.Err()))
	}
	if err != nil && errors.Is(waitErr, context.Canceled) {
		// A malformed response can race with a successful child exit. The
		// private cleanup cancellation must not replace that response failure.
		waitErr = nil
	}
	if err := errors.Join(err, waitErr); err != nil {
		return nil, tokenizerPreviewFailure(err)
	}
	return tokens, nil
}

func writeTokenizerPreviewInput(out io.Writer, text string, encoding byte) error {
	header := make([]byte, 12)
	copy(header, tokenizerPreviewMagic[:])
	header[7] = encoding
	binary.BigEndian.PutUint32(header[8:], uint32(len(text)))
	if n, err := out.Write(header); err != nil {
		return err
	} else if n != len(header) {
		return io.ErrShortWrite
	}
	n, err := io.WriteString(out, text)
	if err == nil && n != len(text) {
		return io.ErrShortWrite
	}
	return err
}

func readTokenizerPreviewOutput(input io.Reader, inputBytes int, encoding byte) ([]tokenizerPreviewToken, error) {
	var header [12]byte
	if _, err := io.ReadFull(input, header[:]); err != nil {
		return nil, err
	}
	want := tokenizerPreviewMagic
	want[7] = encoding
	if string(header[:8]) != string(want[:]) {
		return nil, errors.New("invalid preview response")
	}
	count := binary.BigEndian.Uint32(header[8:])
	if uint64(count) > uint64(inputBytes) {
		return nil, errors.New("invalid preview token count")
	}
	tokens := make([]tokenizerPreviewToken, int(count))
	reader := bufio.NewReaderSize(input, 32*1024)
	var data [8]byte
	var offset uint32
	for i := range tokens {
		if _, err := io.ReadFull(reader, data[:]); err != nil {
			return nil, err
		}
		token := tokenizerPreviewToken{binary.BigEndian.Uint32(data[:4]), binary.BigEndian.Uint32(data[4:])}
		if token.ID > tokenizerPreviewMaximumID(encoding) || token.EndByte <= offset || uint64(token.EndByte) > uint64(inputBytes) {
			return nil, errors.New("invalid preview token boundary")
		}
		tokens[i] = token
		offset = token.EndByte
	}
	if uint64(offset) != uint64(inputBytes) {
		return nil, errors.New("incomplete preview response")
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return nil, errors.New("unexpected preview output")
	}
	return tokens, nil
}

// This action runs only in the disposable helper process. Returning ends that
// process even if its synchronous encoder has not observed parent termination.
func serveTokenizerPreview(ctx context.Context, input io.ReadCloser, out io.Writer) error {
	defer input.Close()
	var header [12]byte
	if _, err := io.ReadFull(input, header[:]); err != nil {
		return err
	}
	encodingID := header[7]
	header[7] = 0
	if string(header[:8]) != string(tokenizerPreviewMagic[:]) || encodingID > 1 {
		return errors.New("invalid preview request")
	}
	size := binary.BigEndian.Uint32(header[8:])
	if size > tokenizer.MaxInputBytes {
		return errors.New("preview input exceeds 1 MiB")
	}
	data := make([]byte, int(size))
	if _, err := io.ReadFull(input, data); err != nil {
		return err
	}
	if !utf8.Valid(data) {
		return errors.New("invalid preview input")
	}
	encoding := "o200k_base"
	if encodingID == 1 {
		encoding = "cl100k_base"
	}
	life := make(chan struct{})
	go func() {
		defer close(life)
		var extra [1]byte
		_, _ = input.Read(extra[:])
	}()
	// Native stdin may use a blocking descriptor that Close cannot interrupt.
	// Do not join its lifeline reader here: returning exits this helper process.
	done := make(chan error, 1)
	go func() {
		result, err := tokenizer.Encode(string(data), encoding, true)
		if err == nil {
			err = writeTokenizerPreviewOutput(out, result, encodingID)
		}
		done <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-life:
		return context.Canceled
	case err := <-done:
		return err
	}
}

func writeTokenizerPreviewOutput(out io.Writer, result tokenizer.Result, encoding byte) error {
	buffer := bufio.NewWriterSize(out, 32*1024)
	var header [12]byte
	copy(header[:], tokenizerPreviewMagic[:])
	header[7] = encoding
	binary.BigEndian.PutUint32(header[8:], uint32(result.TokenCount))
	if _, err := buffer.Write(header[:]); err != nil {
		return err
	}
	var data [8]byte
	var offset uint32
	for i, id := range result.IDs {
		if uint64(id) > uint64(tokenizerPreviewMaximumID(encoding)) {
			return errors.New("invalid preview token ID")
		}
		offset += uint32(len(result.Fragments[i]))
		binary.BigEndian.PutUint32(data[:4], uint32(id))
		binary.BigEndian.PutUint32(data[4:], offset)
		if _, err := buffer.Write(data[:]); err != nil {
			return err
		}
	}
	return buffer.Flush()
}
