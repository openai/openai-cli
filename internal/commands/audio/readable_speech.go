package audio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const speechStreamOperation = "(resource) audio.speech > (method) create"

type speechOutputKey struct{}

// ConfigureSpeech installs speech capture and interactive controls before generated dispatch.
func ConfigureSpeech(root *cli.Command, presentation func(context.Context, *cli.Command) SpeechPresentation) {
	resource := root.Command("audio:speech")
	if resource == nil || resource.Command("create") == nil {
		return
	}
	command := resource.Command("create")
	command.Description += "\n\nRun without command flags in a terminal to open speech controls. " +
		"The form retains audio, text, and instructions in private local history. " +
		"Use audio history, inspect, play, and export for retained results. " +
		"Explicit flags and pipes preserve direct output."
	next := command.Action
	command.Action = func(ctx context.Context, command *cli.Command) error {
		opts := presentation(ctx, command)
		return next(context.WithValue(ctx, speechOutputKey{}, opts), command)
	}
	command.Action = audioWorkbenchWorkflow(command.Action)
}

// WriteSpeech intercepts only this command's capture or SSE response on the shared download path.
// Ordinary audio keeps the existing binary response behavior. The caller owns
// response.Body, including closing it on every handled path.
func WriteSpeech(response *http.Response, stdout io.Writer, outfile string) (bool, string, error) {
	if response == nil || response.Request == nil || response.StatusCode >= 300 {
		return false, "", nil
	}
	if capture, ok := response.Request.Context().Value(audioCaptureKey{}).(*audioCapture); ok {
		return true, "", capture.receive(response)
	}
	opts, ok := response.Request.Context().Value(speechOutputKey{}).(SpeechPresentation)
	if !ok {
		return false, "", nil
	}
	kind, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || kind != "text/event-stream" {
		return false, "", nil
	}
	ctx := response.Request.Context()
	if outfile != "" && outfile != "-" && outfile != "/dev/stdout" {
		message, err := clioutput.WriteManagedDownloadResponse(response, outfile, copySpeechSaveStream)
		return true, message, err
	}
	if outfile != "" || opts.Raw {
		if file, ok := stdout.(*os.File); ok && file == os.Stdout {
			return true, "", clioutput.StreamToStdout(func(out *os.File) error {
				return copySpeechStream(ctx, out, response.Body)
			})
		}
		return true, "", copySpeechStream(ctx, stdout, response.Body)
	}
	iter := &speechEventIterator{reader: bufio.NewReader(response.Body)}
	return true, "", opts.Render(ctx, stdout, iter)
}

// Preserve successful wire streams, including framing and bytes after DONE.
// Failures retain bytes already read, then stop consuming the response using the
// same failure and incomplete-result contract as formatted output.
func copySpeechStream(ctx context.Context, destination io.Writer, source io.Reader) error {
	return copySpeechStreamWithCompletion(ctx, destination, source, false)
}

// An explicit-path speech save needs a speech completion event before success.
func copySpeechSaveStream(ctx context.Context, destination io.Writer, source io.Reader) error {
	return copySpeechStreamWithCompletion(ctx, destination, source, true)
}

func copySpeechStreamWithCompletion(ctx context.Context, destination io.Writer, source io.Reader, requireCompletion bool) error {
	copying := &speechCopyReader{source: source, sink: clioutput.Writer{Context: ctx, Sink: destination}}
	reader := bufio.NewReader(copying)
	iter := clioutput.NewIterator(&speechEventIterator{reader: reader}, clioutput.IteratorOptions{
		Context:   ctx,
		Transform: transformers.Identity,
		Limit:     -1,
		Route:     transformers.Route{Operation: speechStreamOperation, OutputKind: transformers.OutputStreamEvent},
	})
	completed := false
	for iter.Next() {
		completed = completed || iter.Current().Get("type").Str == "speech.audio.done"
	}
	if iter.Err() != nil || copying.err != nil || ctx.Err() != nil {
		return errors.Join(iter.Err(), copying.err, ctx.Err())
	}
	if requireCompletion && !completed {
		return &clioutput.StreamResultError{Message: "The speech stream ended without a completion event."}
	}
	_, tailErr := io.Copy(io.Discard, reader)
	return errors.Join(iter.Err(), tailErr, ctx.Err())
}

// Keep bytes already read available to the parser even if the output failed, so
// an error event in that buffer can retain its classification alongside EPIPE.
// Stop before reading further from the body after any failed or short write.
type speechCopyReader struct {
	source io.Reader
	sink   clioutput.Writer
	err    error
}

func (r *speechCopyReader) Read(data []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if err := r.sink.Context.Err(); err != nil {
		return 0, err
	}
	n, err := r.source.Read(data)
	if n > 0 {
		_, r.err = r.sink.Write(data[:n])
	}
	if r.err != nil {
		if err == nil || err == io.EOF {
			return n, r.err
		}
		return n, errors.Join(err, r.err)
	}
	return n, err
}

// Unlike SDK SSE endpoints, speech previously streamed bytes with no per-line
// or per-event limit. Retain complete JSON and dispatch the final event at EOF.
type speechEventIterator struct {
	reader    *bufio.Reader
	current   clioutput.JSON
	err       error
	resultErr error
	done      bool
	started   bool
	skipLF    bool
}

func (it *speechEventIterator) Next() bool {
	if it.done || it.err != nil || it.resultErr != nil {
		return false
	}
	var data bytes.Buffer
	seenData := false
	eventName := ""
	for {
		line, err := it.readLine()
		if err != nil && !errors.Is(err, io.EOF) {
			it.err = err
		}
		it.done = err != nil
		if !it.started {
			line = strings.TrimPrefix(line, "\ufeff")
			it.started = true
		}
		field, value, _ := strings.Cut(line, ":")
		switch field {
		case "event":
			eventName = strings.TrimPrefix(value, " ")
		case "data":
			if seenData {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(value, " "))
			seenData = true
		}
		if line != "" && !it.done {
			continue
		}
		if !seenData {
			if it.done {
				return false
			}
			eventName = ""
			continue
		}
		raw := bytes.TrimSpace(data.Bytes())
		if bytes.Equal(raw, []byte("[DONE]")) {
			it.done = true
			return false
		}
		if !json.Valid(raw) {
			if it.err == nil {
				it.err = &clioutput.StreamResultError{Message: "could not read a speech stream event as JSON"}
			}
			return false
		}
		it.current = clioutput.JSON{Result: gjson.ParseBytes(raw)}
		if eventName == "error" && transformers.StreamFailure(it.current.Result, transformers.Route{Operation: speechStreamOperation, OutputKind: transformers.OutputStreamEvent}) == "" {
			// An SSE name can identify failure without a JSON type. Preserve the
			// original JSON for presentation, then stop before consuming more.
			it.resultErr = &clioutput.StreamResultError{Message: "the API reported an error while streaming"}
		}
		return true
	}
}

// SSE permits CR, LF and CRLF. Scan buffered chunks rather than imposing a
// scanner ceiling or reading an entire CR-only stream before its first event.
func (it *speechEventIterator) readLine() (string, error) {
	var line []byte
	for {
		if _, err := it.reader.Peek(1); err != nil {
			return string(line), err
		}
		chunk, _ := it.reader.Peek(it.reader.Buffered())
		if it.skipLF {
			it.skipLF = false
			if chunk[0] == '\n' {
				_, _ = it.reader.Discard(1)
				continue
			}
		}
		if end := bytes.IndexAny(chunk, "\r\n"); end >= 0 {
			line = append(line, chunk[:end]...)
			it.skipLF = chunk[end] == '\r'
			_, _ = it.reader.Discard(end + 1)
			return string(line), nil
		}
		line = append(line, chunk...)
		_, _ = it.reader.Discard(len(chunk))
	}
}

func (it *speechEventIterator) Current() clioutput.JSON { return it.current }
func (it *speechEventIterator) Err() error              { return errors.Join(it.err, it.resultErr) }

// SpeechPresentation captures output policy for a single generated speech action.
// Render receives the response context and owns formatted event presentation.
type SpeechPresentation struct {
	Raw    bool
	Render func(context.Context, io.Writer, jsonview.Iterator[clioutput.JSON]) error
}
