//go:build windows

package autocomplete

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func checkPickerFileLinks(file *os.File) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return errors.New("cannot inspect shell integration file links")
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 && info.NumberOfLinks != 1 {
		return errors.New("shell integration file has hard links; existing files were kept")
	}
	return nil
}
