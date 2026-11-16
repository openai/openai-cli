//go:build windows

package terminal

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ConsoleInputRecord represents INPUT_RECORD: a WORD tag, padding, and a
// 16-byte KEY_EVENT_RECORD. Field order and padding preserve the native ABI.
// https://learn.microsoft.com/en-us/windows/console/input-record-str
// https://learn.microsoft.com/en-us/windows/console/key-event-record-str
type ConsoleInputRecord struct {
	EventType    uint16
	_            uint16
	KeyDown      int32
	Repeat       uint16
	VirtualKey   uint16
	ScanCode     uint16
	Character    uint16
	ControlState uint32
}

var consoleReadInputEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputExW")

// ReadConsoleInputRecords reads only available records, without leaving a
// pending read on borrowed stdin.
func ReadConsoleInputRecords(input windows.Handle, records []ConsoleInputRecord) (int, error) {
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

// PrepareConsoleInput checks availability of the nonblocking native record API.
func PrepareConsoleInput() error { return consoleReadInputEx.Find() }
