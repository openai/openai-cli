//go:build !windows

package custom

import (
	"io"

	uv "github.com/charmbracelet/ultraviolet"
)

func newAdminSetupKeyReader(input io.Reader) (adminSetupKeyReader, error) {
	return uv.NewCancelReader(input)
}
