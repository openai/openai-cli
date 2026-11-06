package input

import "os"

// IsPiped reports whether a borrowed file should be treated as piped request input.
// Empty pipes attached by terminals and editors need a short readiness probe;
// classification never reads from or closes the file.
func IsPiped(file *os.File) bool {
	stat, err := file.Stat()
	if err != nil {
		return false
	}

	mode := stat.Mode()

	// Regular file (redirect like < file.txt) — only if non-empty.
	//
	// Notably, on Unix the case like `< /dev/null` is handled below because `/dev/null` is not a regular
	// file. On Windows, NUL appears as a regular file with size 0, so it's also handled correctly.
	if mode.IsRegular() && stat.Size() > 0 {
		return true
	}

	// For pipes/sockets (e.g. `echo foo | openai`), use an OS-specific check to determine whether
	// data is actually available. Some environments like Cursor's integrated terminal connect stdin as a
	// pipe even when nothing is being piped.
	if mode&(os.ModeNamedPipe|os.ModeSocket) != 0 {
		// The platform probe observes readiness without reading or closing the file.
		return pipedDataAvailable(file)
	}

	return false
}
