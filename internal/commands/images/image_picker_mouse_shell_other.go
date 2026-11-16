//go:build !darwin

package images

import (
	"context"
	"os"
)

func imagePickerNativeMouseShell(context.Context, *os.File, *os.File) bool {
	return false
}
