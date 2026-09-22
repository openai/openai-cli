package imageopen

import (
	"context"
	"fmt"
)

func launch(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Absolute paths cannot be interpreted as flags. No shell is involved.
	if err := viewerCommand(ctx, "/usr/bin/open", path, false).Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("request default image viewer: %w", fileErrorCause(err))
	}
	return nil
}
