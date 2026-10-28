package jsonview

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestWriteTerminalTextRuneBoundaries(t *testing.T) {
	for _, offset := range []int{0, 1, 32765, 32766, 32767, 32768} {
		for _, sample := range []struct{ input, want string }{
			{"é世界☕🌍\n\t", "é世界☕🌍\n\t"},
			{"\u009b\u009d\u009c\x1b\a", `\u009b\u009d\u009c\u001b\u0007`},
			{"\xc2\x1b\xe2\x82", "�" + `\u001b` + "��"},
		} {
			t.Run(fmt.Sprintf("offset=%d/input=%q", offset, sample.input), func(t *testing.T) {
				prefix := strings.Repeat("x", offset)
				// DataErrReader returns the last bytes and EOF together.
				source := iotest.DataErrReader(strings.NewReader(prefix + sample.input))
				var output bytes.Buffer
				if err := WriteTerminalText(&output, source); err != nil {
					t.Fatal(err)
				}
				if want := prefix + sample.want; output.String() != want {
					t.Errorf("output differs at rune boundary: got %d bytes, want %d", output.Len(), len(want))
				}
			})
		}
	}
}

func TestWriteTerminalTextEscapesControlRanges(t *testing.T) {
	for r := rune(0); r <= 0x9f; r++ {
		if r >= 0x20 && r < 0x7f || r == '\n' || r == '\t' {
			continue
		}
		var output bytes.Buffer
		if err := WriteTerminalText(&output, iotest.OneByteReader(strings.NewReader(string(r)))); err != nil {
			t.Fatal(err)
		}
		for _, got := range output.String() {
			if got < 0x20 || got >= 0x7f && got <= 0x9f {
				t.Errorf("control U+%04X survived as U+%04X", r, got)
			}
		}
		if output.Len() == 0 {
			t.Errorf("control U+%04X silently dropped", r)
		}
	}
}

func TestWriteTerminalTextShortEscapedWrite(t *testing.T) {
	err := WriteTerminalText(terminalShortWriter{}, strings.NewReader("\x1b"))
	if err != io.ErrShortWrite {
		t.Errorf("error = %v, want %v", err, io.ErrShortWrite)
	}
}

type terminalShortWriter struct{}

func (terminalShortWriter) Write(data []byte) (int, error) {
	return len(data) - 1, nil
}
