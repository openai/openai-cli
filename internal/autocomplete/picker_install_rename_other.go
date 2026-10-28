//go:build !darwin && !linux && !windows

package autocomplete

import (
	"errors"
	"os"
)

func renamePickerEntriesNoReplace(_ *os.File, _ string, _ *os.File, _ string) error {
	return errors.New("safe shell startup recovery is unsupported on this platform")
}
