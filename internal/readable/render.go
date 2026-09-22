package readable

import (
	"crypto/sha256"
	"hash"
	"io"
	"strings"

	"github.com/tidwall/gjson"
)

// WriteResult prints a prepared readable view, or the complete labeled value
// when no specialized view applies.
func WriteResult(out io.Writer, value gjson.Result, view View) error {
	if view.Text.IsText {
		return WriteText(out, view.Text.Text)
	}
	if view.Summary.Exists() {
		value = view.Summary
	}
	if err := Write(out, value); err != nil {
		return err
	}
	if view.Omitted {
		return WriteText(out, "Details: Use --format json for all fields.")
	}
	return nil
}

// WriteText escapes terminal controls and writes one complete text result.
func WriteText(out io.Writer, value string) error {
	text := Text(value)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return writeString(out, text)
}

func writeString(out io.Writer, text string) error {
	n, err := io.WriteString(out, text)
	if err == nil && n != len(text) {
		err = io.ErrShortWrite
	}
	return err
}

type textPart struct {
	digest hash.Hash
	size   int
}

// StreamWriter emits text as events arrive. Each text part keeps only a digest,
// so completion snapshots can be compared with earlier deltas without retaining
// the response text. Iterator consumption and errors remain with the caller.
type StreamWriter struct {
	out         io.Writer
	parts       map[string]*textPart
	textOpen    bool
	emitted     bool
	lineEnded   bool
	previousKey string
}

func NewStreamWriter(out io.Writer) *StreamWriter {
	return &StreamWriter{out: out, parts: make(map[string]*textPart)}
}

// HasOutput reports whether an event produced visible content.
func (w *StreamWriter) HasOutput() bool { return w.emitted }

// Finish closes an unfinished text line without printing an empty-result message.
// The caller should check the iterator's error before reporting an empty result.
func (w *StreamWriter) Finish() error {
	if w.textOpen {
		w.textOpen = false
		if !w.lineEnded {
			return writeString(w.out, "\n")
		}
	}
	return nil
}

func (w *StreamWriter) emit(key, text string, delta, snapshot bool) error {
	part := w.parts[key]
	if snapshot && part != nil && part.size == len(text) {
		digest := sha256.Sum256([]byte(text))
		if string(part.digest.Sum(nil)) == string(digest[:]) {
			return nil
		}
	}
	if w.textOpen && (!delta || w.previousKey != key) {
		if err := w.Finish(); err != nil {
			return err
		}
		if err := writeString(w.out, "\n"); err != nil {
			return err
		}
	} else if !w.textOpen && w.emitted {
		if err := writeString(w.out, "\n"); err != nil {
			return err
		}
	}
	if err := writeString(w.out, Text(text)); err != nil {
		return err
	}
	if part == nil || !delta {
		part = &textPart{digest: sha256.New()}
		w.parts[key] = part
	}
	_, _ = io.WriteString(part.digest, text)
	part.size += len(text)
	if text != "" {
		w.lineEnded = strings.HasSuffix(text, "\n")
	}
	w.textOpen, w.emitted, w.previousKey = true, true, key
	return nil
}

// Write prints one stream event or list record. Text snapshots are deduplicated
// only when the API identifies the same text part; list records remain distinct.
func (w *StreamWriter) Write(value gjson.Result, view View) error {
	projection := view.Text
	if projection.Skip {
		return nil
	}
	if projection.IsText {
		if len(projection.Parts) > 0 {
			for _, part := range projection.Parts {
				if err := w.emit(part.Key, part.Text, false, true); err != nil {
					return err
				}
			}
		} else if err := w.emit(projection.Key, projection.Text, projection.Delta, projection.Snapshot); err != nil {
			return err
		}
		if projection.Details.Exists() {
			if err := w.Finish(); err != nil {
				return err
			}
			if err := writeString(w.out, "\n"); err != nil {
				return err
			}
			return Write(w.out, projection.Details)
		}
		return nil
	}
	if err := w.Finish(); err != nil {
		return err
	}
	if w.emitted {
		if err := writeString(w.out, "\n"); err != nil {
			return err
		}
	}
	if err := WriteResult(w.out, value, view); err != nil {
		return err
	}
	w.emitted = true
	return nil
}
