package terminalimage

import (
	"context"
	"io"
)

// CanceledOutputError retains the transport errors from a deliberately canceled
// interactive transfer. It does not include the subsequent terminal reset.
type CanceledOutputError struct{ cause error }

func (e *CanceledOutputError) Error() string { return e.cause.Error() }
func (e *CanceledOutputError) Unwrap() error { return e.cause }

// WriteControl sends caller-owned terminal controls through the same cancellable
// output binding as native images. Callers must not pass untrusted content.
func WriteControl(ctx context.Context, out io.Writer, control string) error {
	return writeITermOutput(ctx, out, func(destination io.Writer) error {
		_, err := io.WriteString(contextWriter{ctx, destination}, control)
		return err
	})
}

// WriteFrame preserves native graphics reset handling for a caller-owned view.
// Text and image rows must already contain escaped text or prepared image bytes.
func WriteFrame(ctx context.Context, out io.Writer, frame string, protocol string) error {
	if protocol == "kitty" {
		return writeKittyOutput(ctx, out, func(destination io.Writer) error {
			return writeKittyView(ctx, destination, frame)
		})
	}
	return WriteControl(ctx, out, frame)
}
