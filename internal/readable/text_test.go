package readable

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestWriteTextPreservesContentAndNewlines(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ input, want string }{
		{"", "\n"},
		{"complete text", "complete text\n"},
		{"first\n\tsecond\n", "first\n\tsecond\n"},
		{"two final newlines\n\n", "two final newlines\n\n"},
		{"世界\r\x1b[2J\u202e\u2029", "世界" + `\r\u001b[2J\u202e\u2029` + "\n"},
	} {
		t.Run(test.input, func(t *testing.T) {
			var out strings.Builder
			if err := WriteText(&out, test.input); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestWriteTextPropagatesWriterErrorsAndShortWrites(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		fail error
		want error
	}{
		{"error", io.ErrClosedPipe, io.ErrClosedPipe},
		{"short write", nil, io.ErrShortWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := &failingWriter{fail: test.fail}
			if err := WriteText(out, "synthetic result"); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if out.calls != 1 {
				t.Fatalf("writer received %d calls after failure", out.calls)
			}
		})
	}
}
