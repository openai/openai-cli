package binaryparam

import (
	"io"
	"os"
)

const stdinGlyph = "-"

// FileOrStdin opens the file at the given path for reading. If the path is "-", stdin is returned instead.
//
// It's the caller's responsibility to close the returned ReadCloser (usually with `defer`).
//
// Returns a boolean indicating whether stdin is being used. If true, no other components of the calling
// program should attempt to read from stdin for anything else.
func FileOrStdin(stdin io.ReadCloser, path string) (io.ReadCloser, bool, error) {
	// When the special glyph "-" is used, read from stdin. Although probably less necessary, also support
	// special Unix files that refer to stdin.
	switch path {
	case "", stdinGlyph, "/dev/fd/0", "/dev/stdin":
		return stdin, true, nil
	}

	readCloser, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}

	return readCloser, false, err
}
