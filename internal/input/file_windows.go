package input

import (
	"context"
	"os"
)

// ReadFile on Windows retains existing synchronous behavior. The caller's
// handle remains borrowed; cancellation does not close it or change its mode.
func ReadFile(_ context.Context, file *os.File, data []byte) (int, error) {
	return file.Read(data)
}
