//go:build windows

package batches

import (
	"context"
	"errors"
	"os"

	"github.com/openai/openai-cli/internal/windowsfile"
)

// RunBatchDirectoryHelper rejects the Unix-only helper before CLI parsing.
func RunBatchDirectoryHelper(args []string) (bool, error) {
	if len(args) < 2 || args[1] != "__batch-directory" {
		return false, nil
	}
	return true, errors.New("batch directory helper is unavailable on Windows")
}

func createPrivateBatchDirectory(ctx context.Context, path string) (os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := windowsfile.CreatePrivateDirectory(path)
	if err != nil {
		return nil, err
	}
	info, err := directory.Stat()
	return info, errors.Join(err, directory.Close())
}
