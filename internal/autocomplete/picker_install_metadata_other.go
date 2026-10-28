//go:build !windows && !darwin && !linux

package autocomplete

import "os"

func checkPickerFileMetadata(file *os.File) error {
	if err := checkPickerFileMode(file); err != nil {
		return err
	}
	return checkPickerFileLinks(file)
}

func checkPickerReplacementMetadata(*os.Root, string, string, pickerFileSnapshot) error { return nil }
