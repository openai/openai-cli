//go:build !darwin && !linux

package terminalimage

import (
	"context"
	"io"
)

func writeKittyOutput(_ context.Context, out io.Writer, write func(io.Writer) error) error {
	return write(out)
}

func writeITermOutput(_ context.Context, out io.Writer, write func(io.Writer) error) error {
	return write(out)
}

func RunKittyOutputHelper(_ []string) (bool, error) { return false, nil }

func PrepareNativeImageOutput(ctx context.Context, _ io.Writer) (context.Context, func() error) {
	return ctx, func() error { return nil }
}
