//go:build !windows

package autocomplete

import (
	"context"
	"os"
)

func pickerProfileIdentity(ctx context.Context, _ *os.Root, profile string) (string, error) {
	return profile, ctx.Err()
}
