//go:build !darwin && !linux

package terminalimage

import (
	"context"
	"io"
)

func writeKittyOutput(_ context.Context, out io.Writer, write func(io.Writer) error) error {
	return write(out)
}

func RunKittyOutputHelper(_ []string) (bool, error) { return false, nil }
