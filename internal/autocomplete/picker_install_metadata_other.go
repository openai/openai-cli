//go:build !windows && !darwin && !linux

package autocomplete

import "os"

func checkPickerFileMetadata(*os.File) error { return nil }

func checkPickerReplacementMetadata(*os.Root, string, string, pickerFileSnapshot) error { return nil }
