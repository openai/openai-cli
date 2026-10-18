// Package output provides shared output writing and failure classification.
package output

import (
	"context"
	"io"
)

// Writer preserves cancellation separately from failures of the sink.
// Only actual sink errors may be treated as a closed output pipe by the pager.
// Context and Sink must be non-nil.
type Writer struct {
	Context context.Context
	Sink    io.Writer
}

func (w Writer) Write(data []byte) (int, error) {
	if err := w.Context.Err(); err != nil {
		return 0, err
	}
	n, err := w.Sink.Write(data)
	return w.result(n, len(data), err)
}

func (w Writer) WriteString(data string) (int, error) {
	if err := w.Context.Err(); err != nil {
		return 0, err
	}
	n, err := io.WriteString(w.Sink, data)
	return w.result(n, len(data), err)
}

func (w Writer) result(n, size int, err error) (int, error) {
	if err == nil && n != size {
		err = io.ErrShortWrite
	}
	if err != nil {
		return n, &WriteError{Err: err}
	}
	return n, w.Context.Err()
}
