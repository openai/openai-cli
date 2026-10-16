//go:build windows

package custom

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// INPUT_RECORD contains a WORD tag, padding, and a 16-byte KEY_EVENT_RECORD.
// https://learn.microsoft.com/en-us/windows/console/input-record-str
// https://learn.microsoft.com/en-us/windows/console/key-event-record-str
type consoleInputRecord struct {
	eventType    uint16
	_            uint16
	keyDown      int32
	repeat       uint16
	virtualKey   uint16
	scanCode     uint16
	character    uint16
	controlState uint32
}

var consoleReadInputEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputExW")

// Read only available records, without leaving a pending read on borrowed stdin.
func readConsoleInputRecords(input windows.Handle, records []consoleInputRecord) (int, error) {
	if len(records) == 0 {
		return 0, nil
	}
	var count uint32
	// CONSOLE_READ_NOWAIT: https://learn.microsoft.com/en-us/windows/console/readconsoleinputex
	result, _, err := consoleReadInputEx.Call(uintptr(input), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&count)), 0x0002)
	if result == 0 {
		if err == nil || err == windows.ERROR_SUCCESS {
			err = windows.ERROR_INVALID_DATA
		}
		return 0, fmt.Errorf("read console input records: %w", err)
	}
	if count > uint32(len(records)) {
		return 0, fmt.Errorf("read console input records: %w", windows.ERROR_INVALID_DATA)
	}
	return int(count), nil
}
