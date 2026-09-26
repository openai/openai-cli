package custom

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const speechStreamOperation = "(resource) audio.speech > (method) create"

type speechOutputKey struct{}

func configureReadableSpeech(root *cli.Command) {
	resource := root.Command("audio:speech")
	if resource == nil || resource.Command("create") == nil {
		return
	}
	command := resource.Command("create")
	next := command.Action
	command.Action = func(ctx context.Context, command *cli.Command) error {
		root := command.Root()
		opts := ShowJSONOpts{
			Context: ctx, Operation: speechStreamOperation,
			OutputKind: OutputStreamEvent, ExplicitFormat: root.IsSet("format"),
			Format: root.String("format"), RawOutput: root.Bool("raw-output"),
			Transform: root.String("transform"), Title: "audio:speech create",
		}
		return next(context.WithValue(ctx, speechOutputKey{}, opts), command)
	}
}

// Speech shares the generated download path. Intercept only this command's SSE
// response; actual audio keeps the existing binary response behavior. The caller
// owns response.Body, including closing it on every handled path.
func writeReadableSpeech(response *http.Response, stdout io.Writer, outfile string) (bool, string, error) {
	if response == nil || response.Request == nil || response.StatusCode >= 300 {
		return false, "", nil
	}
	opts, ok := response.Request.Context().Value(speechOutputKey{}).(ShowJSONOpts)
	if !ok {
		return false, "", nil
	}
	kind, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || kind != "text/event-stream" {
		return false, "", nil
	}
	opts.Context = response.Request.Context()
	if outfile != "" && outfile != "-" && outfile != "/dev/stdout" {
		file, err := os.OpenFile(outfile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return true, "", err
		}
		copyErr := copySpeechStream(opts.Context, file, response.Body)
		if err := errors.Join(copyErr, file.Close()); err != nil {
			return true, "", err
		}
		return true, fmt.Sprintf("Wrote output to: %s", outfile), nil
	}
	if outfile != "" || opts.Transform == "" && (strings.EqualFold(opts.Format, "raw") || opts.RawOutput) {
		if file, ok := stdout.(*os.File); ok && file == os.Stdout {
			return true, "", streamToStdout(func(out *os.File) error {
				return copySpeechStream(opts.Context, out, response.Body)
			})
		}
		return true, "", copySpeechStream(opts.Context, stdout, response.Body)
	}
	opts.Stdout = stdout
	iter := &speechEventIterator{reader: bufio.NewReader(response.Body)}
	return true, "", ShowJSONIterator(iter, -1, opts)
}

// Preserve successful wire streams, including framing and bytes after DONE.
// Failures retain bytes already read, then stop consuming the response using the
// same failure and incomplete-result contract as formatted output.
func copySpeechStream(ctx context.Context, destination io.Writer, source io.Reader) error {
	copying := &speechCopyReader{source: source, sink: outputWriter{ctx: ctx, out: destination}}
	reader := bufio.NewReader(copying)
	iter := &outputIterator[outputJSON]{
		source: &speechEventIterator{reader: reader}, context: ctx,
		transform: transformers.Identity, remaining: -1,
		route: transformers.Route{Operation: speechStreamOperation, OutputKind: OutputStreamEvent},
	}
	for iter.Next() {
	}
	if iter.Err() != nil || copying.err != nil || ctx.Err() != nil {
		return errors.Join(iter.Err(), copying.err, ctx.Err())
	}
	_, tailErr := io.Copy(io.Discard, reader)
	return errors.Join(iter.Err(), tailErr, ctx.Err())
}

// Keep bytes already read available to the parser even if the output failed, so
// an error event in that buffer can retain its classification alongside EPIPE.
// Stop before reading further from the body after any failed or short write.
type speechCopyReader struct {
	source io.Reader
	sink   outputWriter
	err    error
}

func (r *speechCopyReader) Read(data []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if err := r.sink.ctx.Err(); err != nil {
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
	current   outputJSON
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
				it.err = &streamResultError{"could not read a speech stream event as JSON"}
			}
			return false
		}
		it.current = outputJSON{gjson.ParseBytes(raw)}
		if eventName == "error" && transformers.StreamFailure(it.current.Result, transformers.Route{Operation: speechStreamOperation, OutputKind: OutputStreamEvent}) == "" {
			// An SSE name can identify failure without a JSON type. Preserve the
			// original JSON for presentation, then stop before consuming more.
			it.resultErr = &streamResultError{"the API reported an error while streaming"}
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

func (it *speechEventIterator) Current() outputJSON { return it.current }
func (it *speechEventIterator) Err() error          { return errors.Join(it.err, it.resultErr) }
