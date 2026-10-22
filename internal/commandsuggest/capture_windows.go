package commandsuggest

import "io"

// Windows needs a shell-specific transport rather than POSIX inherited FDs.
func privateSink(fd int) io.WriteCloser { return nil }
