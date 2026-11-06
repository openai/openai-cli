package debugmiddleware

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// sseCapture observes wire frame boundaries without substituting for the SDK's
// decoder. It retains neither complete large lines nor an unbounded event list.
type sseCapture struct {
	options                    CaptureOptions
	remaining, outputRemaining int
	raw                        []byte
	frameBytes                 uint64
	lineBytes                  uint64
	prefix                     [8]byte
	prefixN                    int
	firstLine                  bool
	pendingCR                  bool
	frameEnded                 bool
	hasData                    bool
	frames, events             uint64
	lastEvent                  time.Duration
	omitted                    bool
}

func newSSECapture(options CaptureOptions) *sseCapture {
	return &sseCapture{options: options, remaining: options.MaxBytes, outputRemaining: options.MaxBytes, firstLine: true}
}

func (s *sseCapture) retain(b byte, emit func(string)) {
	s.frameBytes++
	if s.options.SSEFrames {
		if s.remaining > 0 {
			s.raw = append(s.raw, b)
			s.remaining--
		} else {
			s.markOmitted(emit)
		}
	}
}

func (s *sseCapture) observe(p []byte, elapsed time.Duration, emit func(string)) {
	for _, b := range p {
		if s.pendingCR {
			s.pendingCR = false
			if b == '\n' {
				s.retain(b, emit)
				if s.frameEnded {
					s.reportFrame(emit)
				}
				continue
			}
			if s.frameEnded {
				s.reportFrame(emit)
			}
		}
		s.retain(b, emit)
		switch b {
		case '\r':
			s.pendingCR = true
			s.endLine(elapsed, emit)
		case '\n':
			s.endLine(elapsed, emit)
		default:
			s.lineBytes++
			if s.prefixN < len(s.prefix) {
				s.prefix[s.prefixN] = b
				s.prefixN++
			}
		}
	}
}

func (s *sseCapture) endLine(elapsed time.Duration, emit func(string)) {
	prefix := s.prefix[:s.prefixN]
	size := s.lineBytes
	if s.firstLine {
		s.firstLine = false
		if bytes.HasPrefix(prefix, []byte{0xef, 0xbb, 0xbf}) {
			prefix = prefix[3:]
			size -= 3
		}
	}
	if size == 0 {
		s.endFrame(elapsed, emit)
	} else if bytes.HasPrefix(prefix, []byte("data:")) || (size == 4 && bytes.Equal(prefix, []byte("data"))) {
		s.hasData = true
	}
	s.lineBytes = 0
	s.prefixN = 0
}

func (s *sseCapture) endFrame(elapsed time.Duration, emit func(string)) {
	s.frames++
	if s.hasData {
		s.events++
		if s.options.EventTiming && s.events <= uint64(s.options.MaxEvents) {
			emit(fmt.Sprintf("SSE event %d complete frame observed after %d ms (gap %d ms)", s.events,
				elapsed.Round(time.Millisecond).Milliseconds(), (elapsed - s.lastEvent).Round(time.Millisecond).Milliseconds()))
		} else if s.options.EventTiming {
			s.markOmitted(emit)
		}
		s.lastEvent = elapsed
	}
	s.hasData = false
	// A CR completes the data frame immediately. Wait only for an optional LF
	// before rendering its exact escaped line ending, never before event timing.
	if s.pendingCR {
		s.frameEnded = true
		return
	}
	s.reportFrame(emit)
}

func (s *sseCapture) reportFrame(emit func(string)) {
	if s.options.SSEFrames {
		if s.frames <= uint64(s.options.MaxEvents) && s.outputRemaining > 0 {
			preview := "<omitted: frame capture truncated>"
			if uint64(len(s.raw)) == s.frameBytes {
				preview = redactSSEFrame(s.raw)
			}
			if len(preview) <= s.outputRemaining {
				s.outputRemaining -= len(preview)
				emit(fmt.Sprintf("SSE frame %d (redacted; %d bytes): %s", s.frames, s.frameBytes, preview))
			} else {
				s.markOmitted(emit)
				s.outputRemaining = 0
			}
			if uint64(len(s.raw)) != s.frameBytes {
				s.markOmitted(emit)
			}
		} else {
			s.markOmitted(emit)
		}
	}
	s.raw = s.raw[:0]
	s.frameBytes = 0
	s.frameEnded = false
}

func (s *sseCapture) markOmitted(emit func(string)) {
	if !s.omitted {
		s.omitted = true
		emit("SSE capture limit reached; further frame previews or event timings may be omitted")
	}
}

func (s *sseCapture) finish(elapsed time.Duration, emit func(string)) {
	if s.pendingCR {
		s.pendingCR = false
		if s.frameEnded {
			s.reportFrame(emit)
		}
	}
	if s.frameBytes > 0 {
		emit(fmt.Sprintf("SSE incomplete trailing frame (%d bytes); no complete-event timing", s.frameBytes))
	}
	if s.omitted {
		emit(fmt.Sprintf("SSE diagnostics truncated by capture limits (%d frames, %d complete data frames observed)", s.frames, s.events))
	}
	s.raw = nil
}

// Each original line and line ending remains visible. All field values, comment
// text, and unknown field names are redacted. JSON data gets a separate safe view.
func redactSSEFrame(raw []byte) string {
	var wire, data strings.Builder
	first := true
	for len(raw) > 0 {
		i := bytes.IndexAny(raw, "\r\n")
		if i < 0 {
			return "<omitted: incomplete frame>"
		}
		line, ending := raw[:i], raw[i:i+1]
		raw = raw[i+1:]
		if ending[0] == '\r' && len(raw) > 0 && raw[0] == '\n' {
			ending = []byte("\r\n")
			raw = raw[1:]
		}
		if first && bytes.HasPrefix(line, []byte{0xef, 0xbb, 0xbf}) {
			wire.WriteString("\xef\xbb\xbf")
			line = line[3:]
		}
		first = false
		if len(line) > 0 {
			field, value, colon := bytes.Cut(line, []byte(":"))
			name := string(field)
			switch name {
			case "", "data", "event", "id", "retry":
				wire.WriteString(name)
			default:
				wire.WriteString(redactedPlaceholder)
			}
			if colon {
				wire.WriteByte(':')
				if len(value) > 0 && value[0] == ' ' {
					wire.WriteByte(' ')
					value = value[1:]
				}
				wire.WriteString(redactedPlaceholder)
			}
			if name == "data" {
				data.Write(value)
				data.WriteByte('\n')
			}
		}
		wire.Write(ending)
	}
	preview := strconv.QuoteToASCII(wire.String())
	if data.Len() > 0 {
		if strings.TrimSpace(data.String()) == "[DONE]" {
			preview += " data=[DONE]"
		} else {
			preview += " data=" + redactJSONCapture([]byte(data.String()), false)
		}
	}
	return preview
}
