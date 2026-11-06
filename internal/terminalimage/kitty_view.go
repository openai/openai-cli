package terminalimage

import (
	"context"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Prepared rows use ordinary Kitty chunks. Darwin's transport instead requires
// each write to contain complete controls within its smaller terminal budget.
func writeKittyView(ctx context.Context, out io.Writer, view string) error {
	destination := contextWriter{ctx, out}
	framed, ok := out.(interface{ kittyFrameSize() int })
	if !ok {
		_, err := io.WriteString(destination, view)
		return err
	}
	limit := framed.kittyFrameSize()
	var record strings.Builder
	flush := func() error {
		if record.Len() == 0 {
			return ctx.Err()
		}
		_, err := io.WriteString(destination, record.String())
		record.Reset()
		return err
	}
	for len(view) > 0 {
		_, size := utf8.DecodeRuneInString(view)
		if view[0] == '\x1b' {
			var state byte
			_, _, size, state = ansi.DecodeSequenceWc(view, 0, nil)
			if size == 0 || state != ansi.NormalState {
				return errors.New("incomplete control in Kitty view")
			}
		}
		sequence := view[:size]
		view = view[size:]
		if record.Len()+size > limit {
			if err := flush(); err != nil {
				return err
			}
		}
		if size > limit {
			if err := writeKittyViewSequence(ctx, out, sequence, limit); err != nil {
				return err
			}
		} else {
			record.WriteString(sequence)
		}
	}
	return flush()
}

// Reframe prepared base64 without decoding or changing the source PNG. Preserve
// the original chunk's continuation flag after its final smaller chunk.
func writeKittyViewSequence(ctx context.Context, out io.Writer, sequence string, limit int) error {
	if !strings.HasPrefix(sequence, "\x1b_G") || !strings.HasSuffix(sequence, "\x1b\\") {
		return errors.New("Kitty view control exceeds the transport budget")
	}
	header, payload, ok := strings.Cut(sequence[3:len(sequence)-2], ";")
	if !ok || len(payload) == 0 || len(payload)%4 != 0 {
		return errors.New("invalid prepared Kitty payload")
	}
	var options, continuation []string
	more := "m=0"
	for _, option := range strings.Split(header, ",") {
		switch {
		case strings.HasPrefix(option, "m="):
			if option != "m=0" && option != "m=1" {
				return errors.New("invalid prepared Kitty continuation")
			}
			more = option
		case option != "":
			options = append(options, option)
			if strings.HasPrefix(option, "q=") {
				continuation = append(continuation, option)
			}
		}
	}
	destination := contextWriter{ctx, out}
	for len(payload) > 0 {
		// A nonempty payload includes the semicolon in Charm's framing.
		opts := append(options, "m=1")
		headerSize := len(ansi.KittyGraphics([]byte{'x'}, opts...)) - 1
		size := min(len(payload), (limit-headerSize)/4*4)
		if size <= 0 {
			return errors.New("Kitty frame options exceed the transport budget")
		}
		if size == len(payload) {
			opts[len(opts)-1] = more
		}
		if _, err := io.WriteString(destination, ansi.KittyGraphics([]byte(payload[:size]), opts...)); err != nil {
			return err
		}
		payload = payload[size:]
		options = continuation
	}
	return ctx.Err()
}
