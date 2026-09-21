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
			Context: ctx, Operation: "(resource) audio.speech > (method) create",
			OutputKind: OutputStreamEvent, ExplicitFormat: root.IsSet("format"),
			Format: root.String("format"), RawOutput: root.Bool("raw-output"),
			Transform: root.String("transform"), Title: "audio:speech create",
		}
		return next(context.WithValue(ctx, speechOutputKey{}, opts), command)
	}
}

// Speech shares the generated binary-response path with downloads. Select event
// presentation only for this command's SSE response, leaving actual audio and
// explicitly selected output destinations byte-for-byte unchanged. SSE failure
// events affect the exit status even when the original bytes are being saved.
func writeReadableSpeech(response *http.Response, stdout io.Writer, outfile string) (bool, string, error) {
	if response == nil || response.Request == nil || response.StatusCode >= 300 {
		return false, "", nil
	}
	opts, ok := response.Request.Context().Value(speechOutputKey{}).(ShowJSONOpts)
	if !ok {
		return false, "", nil
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if kind != "text/event-stream" {
		return false, "", nil
	}
	if outfile != "" && outfile != "-" && outfile != "/dev/stdout" {
		file, err := os.OpenFile(outfile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return true, "", err
		}
		copyErr := copySpeechStream(file, response.Body)
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return true, "", err
		}
		return true, fmt.Sprintf("Wrote output to: %s", outfile), nil
	}
	if outfile != "" || strings.EqualFold(opts.Format, "raw") || opts.RawOutput && opts.Transform == "" {
		return true, "", copySpeechStream(stdout, response.Body)
	}
	opts.Stdout = stdout
	iter := &speechEventIterator{reader: bufio.NewReader(response.Body)}
	return true, "", ShowJSONIterator(iter, -1, opts)
}

// Preserve comments, IDs, framing and trailing bytes while checking the events.
// The parser has no new line/event limit and does not buffer the whole stream.
func copySpeechStream(destination io.Writer, source io.Reader) error {
	sink := &speechCopyWriter{writer: destination}
	reader := bufio.NewReader(io.TeeReader(source, sink))
	iter := &speechEventIterator{reader: reader}
	var failure error
	for iter.Next() {
		if message := transformers.StreamFailure(iter.Current().Result); message != "" && failure == nil {
			failure = errors.New(message)
		}
	}
	if sink.err != nil {
		return errors.Join(failure, iter.Err(), sink.err)
	}
	_, tailErr := io.Copy(io.Discard, reader)
	return errors.Join(failure, iter.Err(), tailErr)
}

// TeeReader trusts the writer's byte count. Report a short write immediately
// rather than silently dropping bytes or continuing to consume the response.
type speechCopyWriter struct {
	writer io.Writer
	err    error
}

func (w *speechCopyWriter) Write(value []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.writer.Write(value)
	if err == nil && n != len(value) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}

// Speech previously streamed bytes without a per-line or per-event size limit.
// Read incrementally without adding the scanner ceiling used by other SDK SSE
// endpoints. Each event retains its complete original JSON, including new fields.
type speechEventIterator struct {
	reader    *bufio.Reader
	current   outputJSON
	err       error
	resultErr error
	done      bool
}

func (it *speechEventIterator) Next() bool {
	if it.done || it.err != nil {
		return false
	}
	var data bytes.Buffer
	seenData := false
	eventName := ""
	for {
		line, err := it.reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			it.err = err
			return false
		}
		it.done = errors.Is(err, io.EOF)
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
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
			it.err = errors.New("could not read a speech stream event as JSON")
			return false
		}
		it.current = outputJSON{gjson.ParseBytes(raw)}
		if eventName == "error" && it.resultErr == nil && transformers.StreamFailure(it.current.Result) == "" {
			// The SSE name can identify an error even when the JSON has no type.
			// Keep this outcome separately so the original event is still emitted
			// unchanged before the caller receives its unsuccessful status.
			it.resultErr = errors.New("the API reported an error while streaming")
		}
		return true
	}
}

func (it *speechEventIterator) Current() outputJSON { return it.current }
func (it *speechEventIterator) Err() error          { return errors.Join(it.err, it.resultErr) }
