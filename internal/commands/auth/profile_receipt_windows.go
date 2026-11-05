//go:build windows

package auth

import "io"

// Windows returns a write error directly for a closed output pipe.
func writeProfileReceipt(writer io.Writer, write func(io.Writer) error) error { return write(writer) }
