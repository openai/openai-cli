package readable

import (
	"bytes"
	"crypto/sha256"
	"hash"
	"io"
	"strings"

	"github.com/tidwall/gjson"
)

// StreamPart describes one identified text component. A snapshot contains its
// complete text so far; other parts append a delta. Labels distinguish alternatives.
type StreamPart struct {
	Key, Text, Label string
	Snapshot         bool
}

// StreamEvent carries projected text and any accompanying structured details.
// An empty event represents progress without visible content.
type StreamEvent struct {
	Parts   []StreamPart
	Details gjson.Result
}

type streamTextPart struct {
	digest hash.Hash
	size   int
}

// StreamWriter emits text immediately and retains only a digest per component.
// It never consumes an iterator or chooses which API fields to display.
type StreamWriter struct {
	out         io.Writer
	parts       map[string]*streamTextPart
	previousKey string
	textOpen    bool
	lineEnded   bool
	emitted     bool
}

func NewStreamWriter(out io.Writer) *StreamWriter {
	return &StreamWriter{out: out, parts: make(map[string]*streamTextPart)}
}

func (w *StreamWriter) HasOutput() bool { return w.emitted }

func (w *StreamWriter) write(text string) error {
	n, err := io.WriteString(w.out, text)
	if err == nil && n != len(text) {
		err = io.ErrShortWrite
	}
	return err
}

// Finish closes partial text before the caller reports completion or an error.
func (w *StreamWriter) Finish() error {
	if w.textOpen {
		w.textOpen = false
		if !w.lineEnded {
			return w.write("\n")
		}
	}
	return nil
}

func (w *StreamWriter) emit(value StreamPart) error {
	part := w.parts[value.Key]
	text := value.Text
	revised := false
	if value.Snapshot && part != nil {
		if len(text) >= part.size {
			prefix := sha256.Sum256([]byte(text[:part.size]))
			if bytes.Equal(prefix[:], part.digest.Sum(nil)) {
				text = text[part.size:]
			} else {
				revised = true
			}
		} else {
			revised = true
		}
	}
	if text == "" && !revised {
		return nil
	}
	continuation := w.textOpen && w.previousKey == value.Key && !revised
	if !continuation {
		if err := w.Finish(); err != nil {
			return err
		}
		if w.emitted {
			if err := w.write("\n"); err != nil {
				return err
			}
		}
		label := value.Label
		if revised {
			if label == "" {
				label = "Updated text"
			} else {
				label = "Updated " + label
			}
		} else if label == "" && w.emitted && w.previousKey != value.Key {
			label = "Text"
		}
		if label != "" {
			if err := w.write(Text(label) + ":\n"); err != nil {
				return err
			}
		}
	}
	if err := w.write(Text(text)); err != nil {
		return err
	}
	if part == nil || revised {
		part = &streamTextPart{digest: sha256.New()}
		w.parts[value.Key] = part
	}
	_, _ = io.WriteString(part.digest, text)
	part.size += len(text)
	w.lineEnded = strings.HasSuffix(text, "\n")
	w.textOpen, w.emitted, w.previousKey = true, true, value.Key
	return nil
}

func (w *StreamWriter) Write(event StreamEvent) error {
	for _, part := range event.Parts {
		if err := w.emit(part); err != nil {
			return err
		}
	}
	if event.Details.Exists() {
		return w.WriteValue(event.Details)
	}
	return nil
}

// WriteValue preserves unfamiliar or non-text content as ordinary labeled data.
func (w *StreamWriter) WriteValue(value gjson.Result) error {
	if err := w.Finish(); err != nil {
		return err
	}
	if w.emitted {
		if err := w.write("\n"); err != nil {
			return err
		}
	}
	if err := Write(w.out, value); err != nil {
		return err
	}
	w.emitted = true
	return nil
}
