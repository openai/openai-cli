package batches

import (
	"context"
	"os"
)

// Batch borrows process stdin. Parsing stays synchronous and retains the same
// exclusive-consumer requirement as other whole-request input.
type batchRequestInput struct {
	ctx  context.Context
	file *os.File
}

func (input batchRequestInput) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if err := input.ctx.Err(); err != nil {
		return 0, err
	}
	return readBatchRequestInput(input.ctx, input.file, data)
}
