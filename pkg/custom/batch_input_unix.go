//go:build !windows

package custom

import (
	"context"
	"os"
)

func readBatchRequestInput(ctx context.Context, file *os.File, data []byte) (int, error) {
	// Reuse the existing polling path that preserves borrowed descriptor flags,
	// caller deadlines, closure, and stream bytes.
	return readTokenizerFile(ctx, file, data)
}
