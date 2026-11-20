package audio

import (
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/audiolive"
)

type terminalCaptionTime struct {
	service  *float64
	observed time.Time
}

func (t *terminalCaptionTime) observe(event audiolive.Event) {
	if t.observed.IsZero() {
		t.service, t.observed = event.ElapsedMS, time.Now()
	}
}

func (t terminalCaptionTime) delayUntil(next terminalCaptionTime) (time.Duration, bool) {
	if t.observed.IsZero() || next.observed.IsZero() {
		return 0, false
	}
	delay := next.observed.Sub(t.observed)
	if t.service != nil && next.service != nil {
		delay = time.Duration((*next.service - *t.service) * float64(time.Millisecond))
	}
	return delay, delay >= 0
}

// Source transcripts can interleave partial text and corrections for several turns.
// Keep fragment bytes on disk. The index preserves first-seen turn order at export.
type terminalInputCaptions struct {
	file      *os.File
	end       int64
	anonymous int
	index     map[terminalCaptionItem]*terminalCaptionTurn
	turns     []*terminalCaptionTurn
	tail      string
}

type terminalCaptionItem struct {
	id               string
	index, anonymous int
}
type terminalCaptionSpan struct{ start, end int64 }
type terminalCaptionTurn struct {
	spans        []terminalCaptionSpan
	replacements int
	final        bool
}

func newTerminalInputCaptions(file *os.File) *terminalInputCaptions {
	return &terminalInputCaptions{file: file, index: make(map[terminalCaptionItem]*terminalCaptionTurn)}
}

func (c *terminalInputCaptions) receive(event audiolive.Event) (string, error) {
	final := event.Type == "session.input_transcript.completed"
	text := event.Delta
	if final {
		if event.Transcript == nil {
			return c.tail, nil
		}
		text = *event.Transcript
	}
	id := terminalCaptionItem{id: event.ItemID, index: event.ContentIndex}
	if event.ItemID == "" {
		id.anonymous = c.anonymous
		if final {
			c.anonymous++
		}
	}
	turn := c.index[id]
	if turn == nil {
		if text == "" {
			return c.tail, nil
		}
		turn = &terminalCaptionTurn{}
		c.index[id] = turn
		c.turns = append(c.turns, turn)
	}
	if turn.final && !final {
		return c.tail, nil
	}
	if (event.ItemID != "" || len(c.turns) > 1) && (final || len(turn.spans) == 0) {
		text = strings.TrimLeftFunc(text, unicode.IsSpace)
	}
	replacements := strings.Count(text, "�")
	if final {
		turn.final = true
		if len(turn.spans) > 0 && replacements > turn.replacements {
			return c.tail, nil
		}
	}
	n, err := c.file.WriteString(text)
	if err != nil {
		return "", err
	}
	start := c.end
	c.end += int64(n)
	appendTail := !final && turn == c.turns[len(c.turns)-1]
	if appendTail && len(turn.spans) == 0 && c.tail != "" && n > 0 {
		c.tail = terminalTranslationTail(c.tail, " ")
	}
	if final {
		turn.spans = turn.spans[:0]
		turn.replacements = 0
	}
	if n > 0 {
		if count := len(turn.spans); count > 0 && turn.spans[count-1].end == start {
			turn.spans[count-1].end = c.end
		} else {
			turn.spans = append(turn.spans, terminalCaptionSpan{start, c.end})
		}
	}
	turn.replacements += replacements
	if appendTail {
		c.tail = terminalTranslationTail(c.tail, text)
	} else {
		c.tail, err = c.readTail()
	}
	return c.tail, err
}

// Read only the visible suffix when an earlier turn changes.
func (c *terminalInputCaptions) readTail() (string, error) {
	const limit = 4096
	var buffer [limit + 1]byte
	pos := len(buffer)
	for _, turn := range slices.Backward(c.turns) {
		if pos == 0 {
			break
		}
		if len(turn.spans) == 0 {
			continue
		}
		if pos < len(buffer) {
			pos--
			buffer[pos] = ' '
		}
		for _, span := range slices.Backward(turn.spans) {
			if pos == 0 {
				break
			}
			n := int(min(int64(pos), span.end-span.start))
			if _, err := c.file.ReadAt(buffer[pos-n:pos], span.end-int64(n)); err != nil {
				return "", err
			}
			pos -= n
		}
	}
	// Do not split a UTF-8 character at the left edge.
	if pos == 0 {
		pos++
		for pos < len(buffer) && !utf8.RuneStart(buffer[pos]) {
			pos++
		}
		return "…" + string(buffer[pos:]), nil
	}
	return string(buffer[pos:]), nil
}

func (c *terminalInputCaptions) writeTo(ctx context.Context, dst io.Writer) error {
	separate := false
	for _, turn := range c.turns {
		if len(turn.spans) == 0 {
			continue
		}
		if separate {
			if _, err := io.WriteString(dst, " "); err != nil {
				return err
			}
		}
		for _, span := range turn.spans {
			reader := io.NewSectionReader(c.file, span.start, span.end-span.start)
			if err := terminalTranslationCopy(ctx, dst, reader); err != nil {
				return err
			}
		}
		separate = true
	}
	return nil
}
