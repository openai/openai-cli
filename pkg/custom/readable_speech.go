package custom

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

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
// explicitly selected output destinations byte-for-byte unchanged.
func writeReadableSpeech(response *http.Response, stdout io.Writer, outfile string) (bool, error) {
	if response == nil || response.Request == nil || outfile != "" || response.StatusCode >= 300 {
		return false, nil
	}
	opts, ok := response.Request.Context().Value(speechOutputKey{}).(ShowJSONOpts)
	if !ok || strings.EqualFold(opts.Format, "raw") || opts.RawOutput && opts.Transform == "" {
		return false, nil
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if kind != "text/event-stream" {
		return false, nil
	}
	opts.Stdout = stdout
	iter := &speechEventIterator{reader: bufio.NewReader(response.Body)}
	return true, ShowJSONIterator(iter, -1, opts)
}

// Speech previously streamed bytes without a per-line or per-event size limit.
// Read incrementally without adding the scanner ceiling used by other SDK SSE
// endpoints. Each event retains its complete original JSON, including new fields.
type speechEventIterator struct {
	reader  *bufio.Reader
	current outputJSON
	err     error
	done    bool
}

func (it *speechEventIterator) Next() bool {
	if it.done || it.err != nil {
		return false
	}
	var data bytes.Buffer
	seenData := false
	for {
		line, err := it.reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			it.err = err
			return false
		}
		it.done = errors.Is(err, io.EOF)
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if field, value, _ := strings.Cut(line, ":"); field == "data" {
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
		return true
	}
}

func (it *speechEventIterator) Current() outputJSON { return it.current }
func (it *speechEventIterator) Err() error          { return it.err }
