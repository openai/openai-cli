package custom

import (
	"context"
	"os"
)

// Windows file reads retain their existing synchronous behavior. The caller's
// handle remains borrowed; cancellation does not close it or change its mode.
func readTokenizerFile(_ context.Context, file *os.File, data []byte) (int, error) {
	return file.Read(data)
}
