package request

import (
	"fmt"
	"io"
)

// OnceStdinReader wraps an io.Reader that can only be consumed once, used to ensure stdin is read by at most
// one parameter (or only for a body root parameter or only for YAML parameter input). If FailureReason is set, stdin
// is unavailable and Take() returns an error explaining why.
type OnceStdinReader struct {
	Reader        io.Reader
	FailureReason string
}

// Take claims the single stdin consumer without closing the borrowed reader.
func (o *OnceStdinReader) Take() (io.Reader, error) {
	if o.FailureReason != "" {
		return nil, fmt.Errorf("cannot read from stdin: %s", o.FailureReason)
	}
	if o.Reader == nil {
		return nil, fmt.Errorf("stdin has already been read by another parameter; it can only be read once")
	}
	r := o.Reader
	o.Reader = nil
	return r, nil
}

// ReadAll claims stdin and reads its contents without closing the borrowed reader.
func (o *OnceStdinReader) ReadAll() ([]byte, error) {
	r, err := o.Take()
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}
