package imageopen

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"

	"golang.org/x/sys/windows"
)

func launch(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return errors.New("image path contains an invalid character")
	}
	verb, _ := windows.UTF16PtrFromString("open")
	// ShellExecute can use COM extensions. Keep initialization, execution and
	// cleanup on one thread, as required by the Windows shell contract.
	// https://learn.microsoft.com/windows/win32/api/shellapi/nf-shellapi-shellexecutew
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	if err != nil && !errors.Is(err, syscall.Errno(windows.S_FALSE)) {
		return fmt.Errorf("initialize Windows image viewer: %w", err)
	}
	defer windows.CoUninitialize()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("request default image viewer: %w", err)
	}
	return nil
}
