package terminal

import "os"

// OpenPrivateOutput is unavailable on Windows.
func OpenPrivateOutput(_ int, _ string) *os.File { return nil }
