//go:build !windows

package custom

import (
	"os"

	uv "github.com/charmbracelet/ultraviolet"
)

func newTokenizerInputSource(input *os.File) (tokenizerCancelableInput, error) {
	return uv.NewCancelReader(input)
}
