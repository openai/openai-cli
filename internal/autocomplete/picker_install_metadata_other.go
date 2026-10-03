//go:build !windows && !darwin && !linux

package autocomplete

import "os"

func checkPickerFileMetadata(file *os.File) error { return checkPickerFileLinks(file) }

func checkPickerReplacementMetadata(*os.Root, string, string, pickerFileSnapshot) error { return nil }
