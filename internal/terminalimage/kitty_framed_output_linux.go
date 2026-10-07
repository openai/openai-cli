package terminalimage

import (
	"errors"
	"io"
	"os"
)

func copyKittyFrames(_ *os.File, _ io.Reader) error {
	return errors.New("framed Kitty output is only used on Darwin")
}
