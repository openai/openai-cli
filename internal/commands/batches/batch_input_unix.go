//go:build !windows

package batches

import (
	"context"
	"os"

	cliinput "github.com/openai/openai-cli/internal/input"
)

func readBatchRequestInput(ctx context.Context, file *os.File, data []byte) (int, error) {
	// Reuse the existing polling path that preserves borrowed descriptor flags,
	// caller deadlines, closure, and stream bytes.
	return cliinput.ReadFile(ctx, file, data)
}
