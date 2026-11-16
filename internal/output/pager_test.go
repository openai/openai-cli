package output

import (
	"errors"
	"os"
	"testing"
)

func TestStreamOutput(t *testing.T) {
	t.Setenv("PAGER", "cat")
	err := Stream("stream test", func(w *os.File) error {
		_, writeErr := w.WriteString("Hello world\n")
		return writeErr
	})
	if err != nil {
		t.Errorf("Stream failed: %v", err)
	}
}

func TestPagerErrorPreservesCause(t *testing.T) {
	if got := wrapPagerError(nil); got != nil {
		t.Errorf("wrapped nil = %v", got)
	}
	cause := &os.PathError{Op: "open", Path: "fake-sensitive-path", Err: os.ErrNotExist}
	err := wrapPagerError(cause)
	var pathError *os.PathError
	if !errors.Is(err, os.ErrNotExist) || !errors.As(err, &pathError) || pathError != cause || err.Error() != cause.Error() {
		t.Errorf("wrapped error lost its cause: %v", err)
	}
}
