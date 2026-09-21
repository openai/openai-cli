package custom

import (
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"strings"

	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Auto means readable on every destination. Extraction is an explicit request
// for data and keeps its existing JSON/raw-string contract.
func resolvedOutputFormat(opts ShowJSONOpts) string {
	format := strings.ToLower(opts.Format)
	if format == "auto" || format == "" {
		if opts.Transform != "" || opts.RawOutput {
			return "json"
		}
		return "text"
	}
	return format
}

func configureReadableOutput(root *cli.Command) {
	configureReadableAudio(root)
	configureReadableSpeech(root)
	for _, flag := range root.Flags {
		if value, ok := flag.(*cli.StringFlag); ok {
			switch value.Name {
			case "format":
				value.Usage = "Readable text by default, including pipes. Use json for full API data. Formats: " + strings.Join(OutputFormats, ", ")
			case "format-error":
				value.Usage = "Error format on stderr; readable by default. Use json for API error details."
			}
		}
	}
}

func writeReadableResult(out io.Writer, value gjson.Result, opts ShowJSONOpts) error {
	projection := transformers.Readable(value, transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind})
	if projection.IsText {
		return writeReadableText(out, projection.Text)
	}
	return readable.Write(out, value)
}

func writeReadableText(out io.Writer, value string) error {
	text := readable.Text(value)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	n, err := io.WriteString(out, text)
	if err == nil && n != len(text) {
		err = io.ErrShortWrite
	}
	return err
}

// Text is written as each event arrives, without preloading a screen or opening
// a pager. Each text part keeps only a digest, so completion snapshots can be
// compared with deltas without retaining the response text in memory.
func showReadableIterator[T any](iter jsonview.Iterator[T], opts ShowJSONOpts) error {
	type textPart struct {
		digest hash.Hash
		size   int
	}
	parts := make(map[string]*textPart)
	textOpen, emitted, lineEnded, previousKey := false, false, false, ""
	write := func(text string) error {
		n, err := io.WriteString(opts.Stdout, text)
		if err == nil && n != len(text) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return &outputWriteError{err}
		}
		return nil
	}
	finishText := func() error {
		if textOpen {
			textOpen = false
			if !lineEnded {
				return write("\n")
			}
		}
		return nil
	}
	emit := func(key, text string, delta, snapshot bool) error {
		part := parts[key]
		if snapshot && part != nil && part.size == len(text) {
			digest := sha256.Sum256([]byte(text))
			if string(part.digest.Sum(nil)) == string(digest[:]) {
				return nil
			}
		}
		if textOpen && (!delta || previousKey != key) {
			if err := finishText(); err != nil {
				return err
			}
			if err := write("\n"); err != nil {
				return err
			}
		} else if !textOpen && emitted {
			if err := write("\n"); err != nil {
				return err
			}
		}
		if err := write(readable.Text(text)); err != nil {
			return err
		}
		if part == nil || !delta {
			part = &textPart{digest: sha256.New()}
			parts[key] = part
		}
		_, _ = io.WriteString(part.digest, text)
		part.size += len(text)
		if text != "" {
			lineEnded = strings.HasSuffix(text, "\n")
		}
		textOpen, emitted, previousKey = true, true, key
		return nil
	}
	var completionErr error
	for iter.Next() {
		item, ok := any(iter.Current()).(hasRawJSON)
		if !ok {
			return fmt.Errorf("readable output requires normalized JSON values")
		}
		value := applyJSONPath(gjson.Parse(item.RawJSON()), opts.Transform)
		projection := transformers.Readable(value, transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind})
		if projection.Skip {
			continue
		}
		if projection.IsText {
			if len(projection.Parts) > 0 {
				for _, part := range projection.Parts {
					if err := emit(part.Key, part.Text, false, true); err != nil {
						return err
					}
				}
			} else if err := emit(projection.Key, projection.Text, projection.Delta, projection.Snapshot); err != nil {
				return err
			}
			if projection.Details.Exists() {
				if err := finishText(); err != nil {
					return err
				}
				if err := write("\n"); err != nil {
					return err
				}
				if err := readable.Write(opts.Stdout, projection.Details); err != nil {
					return &outputWriteError{err}
				}
			}
			continue
		}
		if err := finishText(); err != nil {
			return err
		}
		if emitted {
			if err := write("\n"); err != nil {
				return err
			}
		}
		if err := readable.Write(opts.Stdout, value); err != nil {
			return &outputWriteError{err}
		}
		if opts.OutputKind == OutputStreamEvent {
			switch value.Get("type").String() {
			case "response.failed", "response.incomplete", "error":
				completionErr = fmt.Errorf("the streamed response did not complete successfully")
			}
		}
		emitted = true
	}
	if err := finishText(); err != nil {
		return err
	}
	if err := iter.Err(); err != nil {
		return err
	}
	if completionErr != nil {
		return completionErr
	}
	if !emitted {
		return write("No results.\n")
	}
	return nil
}
