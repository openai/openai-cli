//go:build windows

package custom

import (
	"context"
	"io"
	"os"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// INPUT_RECORD contains a WORD tag, padding, and a 16-byte KEY_EVENT_RECORD.
// https://learn.microsoft.com/en-us/windows/console/input-record-str
// https://learn.microsoft.com/en-us/windows/console/key-event-record-str
type adminSetupConsoleInputRecord struct {
	eventType    uint16
	_            uint16
	keyDown      int32
	repeat       uint16
	virtualKey   uint16
	scanCode     uint16
	character    uint16
	controlState uint32
}

var adminSetupReadConsoleInputEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputExW")

type adminSetupConsoleReader struct {
	input        windows.Handle // Borrowed. Never close shared stdin.
	originalMode uint32
	canceled     atomic.Bool
	closed       atomic.Bool
	reading      atomic.Bool
	records      [32]adminSetupConsoleInputRecord
	count, next  int
	pending      byte
	repeats      uint32
	altNumpad    bool
}

func newAdminSetupKeyReader(input io.Reader) (adminSetupKeyReader, error) {
	file, ok := input.(*os.File)
	if !ok || file == nil {
		return nil, errAdminSetupKeyIO
	}
	if err := adminSetupReadConsoleInputEx.Find(); err != nil {
		return nil, errAdminSetupKeyIO
	}
	reader := &adminSetupConsoleReader{input: windows.Handle(file.Fd())}
	if err := windows.GetConsoleMode(reader.input, &reader.originalMode); err != nil {
		return nil, errAdminSetupKeyIO
	}
	// Keep hidden VT input while reading records without pending ReadFile calls.
	mode := uint32(windows.ENABLE_VIRTUAL_TERMINAL_INPUT | windows.ENABLE_WINDOW_INPUT | windows.ENABLE_EXTENDED_FLAGS)
	if err := windows.SetConsoleMode(reader.input, mode); err != nil {
		return nil, errAdminSetupKeyIO
	}
	if err := reader.discardPendingSnapshot(); err != nil {
		_ = windows.SetConsoleMode(reader.input, reader.originalMode)
		reader.clearInput()
		return nil, errAdminSetupKeyIO
	}
	return reader, nil
}

func (reader *adminSetupConsoleReader) Read(output []byte) (int, error) {
	if len(output) == 0 {
		return 0, nil
	}
	if !reader.reading.CompareAndSwap(false, true) {
		return 0, errAdminSetupKeyIO
	}
	defer reader.reading.Store(false)
	for {
		if reader.canceled.Load() || reader.closed.Load() {
			reader.clearInput()
			return 0, context.Canceled
		}
		if n := reader.copyRecords(output); n > 0 {
			if reader.canceled.Load() {
				clear(output[:n])
				reader.clearInput()
				return 0, context.Canceled
			}
			return n, nil
		}
		reader.clearRecords()
		count, err := readAdminSetupConsoleRecords(reader.input, reader.records[:])
		if err != nil {
			reader.clearInput()
			return 0, errAdminSetupKeyIO
		}
		reader.count = count
		if reader.canceled.Load() {
			reader.clearInput()
			return 0, context.Canceled
		}
		if count != 0 {
			continue
		}
		// A finite wait also bounds cancellation if event signaling is unreliable.
		state, err := windows.WaitForSingleObject(reader.input, 10)
		if err != nil {
			return 0, errAdminSetupKeyIO
		}
		if state != windows.WAIT_OBJECT_0 && state != uint32(windows.WAIT_TIMEOUT) {
			return 0, errAdminSetupKeyIO
		}
	}
}

func (reader *adminSetupConsoleReader) Cancel() bool {
	reader.canceled.Store(true)
	return true // The finite read loop cooperatively observes this request.
}

func (reader *adminSetupConsoleReader) Close() error {
	reader.canceled.Store(true)
	if reader.reading.Load() {
		return errAdminSetupKeyIO
	}
	if reader.closed.Swap(true) {
		return nil
	}
	reader.clearInput()
	drainErr := reader.discardPendingSnapshot()
	restoreErr := windows.SetConsoleMode(reader.input, reader.originalMode)
	if drainErr != nil || restoreErr != nil {
		return errAdminSetupKeyIO
	}
	return nil
}

// Discard only a finite count snapshot. Later arrivals are outside this guarantee;
// coalescing or competing readers can also complicate individual record identity.
func (reader *adminSetupConsoleReader) discardPendingSnapshot() error {
	var remaining uint32
	if windows.GetNumberOfConsoleInputEvents(reader.input, &remaining) != nil {
		return errAdminSetupKeyIO
	}
	var batch [32]adminSetupConsoleInputRecord
	defer clear(batch[:])
	for remaining != 0 {
		limit := min(remaining, uint32(len(batch)))
		count, err := readAdminSetupConsoleRecords(reader.input, batch[:int(limit)])
		clear(batch[:])
		if err != nil || count == 0 {
			return errAdminSetupKeyIO
		}
		remaining -= uint32(count)
	}
	return nil
}

func readAdminSetupConsoleRecords(input windows.Handle, records []adminSetupConsoleInputRecord) (int, error) {
	var count uint32
	// NOWAIT consumes only available records. No pending ReadFile request exists.
	// https://learn.microsoft.com/en-us/windows/console/readconsoleinputex
	result, _, _ := adminSetupReadConsoleInputEx.Call(uintptr(input), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&count)), 0x0002)
	if result == 0 {
		return 0, errAdminSetupKeyIO
	}
	if count > uint32(len(records)) {
		return 0, errAdminSetupKeyIO
	}
	return int(count), nil
}

func (reader *adminSetupConsoleReader) clearRecords() {
	clear(reader.records[:])
	reader.count, reader.next = 0, 0
}

func (reader *adminSetupConsoleReader) clearInput() {
	reader.clearRecords()
	reader.pending, reader.repeats = 0, 0
	reader.altNumpad = false
}

func (reader *adminSetupConsoleReader) copyRecords(output []byte) int {
	written := 0
	for written < len(output) {
		if reader.repeats != 0 {
			output[written] = reader.pending
			written++
			reader.repeats--
			if reader.repeats == 0 {
				reader.pending = 0
			}
			continue
		}
		if reader.next == reader.count {
			break
		}
		record := reader.records[reader.next]
		reader.records[reader.next] = adminSetupConsoleInputRecord{}
		reader.next++
		value, emit := reader.recordCharacter(record)
		if emit {
			reader.pending, reader.repeats = value, uint32(max(record.repeat, 1))
		}
	}
	return written
}

// Preserve ASCII input and reject unsupported text before the key parser.
// - Preserve translated ASCII, repeats, and character-bearing Alt release.
// - Ignore only non-text records and identified modifier/composition transitions.
// - Emit a rejected non-ASCII byte for unsafe or ambiguous text-bearing input.
// Console VT translation normally removes physical modifier flags before storage.
// Reject ambiguous raw Alt input rather than accepting a changed key.
func (reader *adminSetupConsoleReader) recordCharacter(record adminSetupConsoleInputRecord) (byte, bool) {
	if record.eventType != 1 {
		return 0, false
	}
	alt := record.controlState&0x0003 != 0
	ctrl := record.controlState&0x000c != 0
	isAlt := record.virtualKey == 0x12 || record.virtualKey == 0xa4 || record.virtualKey == 0xa5
	if record.keyDown == 0 {
		if !isAlt {
			return 0, false
		}
		composed := reader.altNumpad
		reader.altNumpad = false
		if record.character == 0 && !composed {
			return 0, false
		}
		// Alt-numpad produces its composed character on Alt release.
		if record.character == 0 || record.character > 0x7f {
			return 0x80, true
		}
		return byte(record.character), true
	}
	if record.character == 0 && adminSetupConsoleModifier(record.virtualKey) {
		return 0, false
	}
	if alt && !ctrl && record.virtualKey >= 0x60 && record.virtualKey <= 0x69 {
		reader.altNumpad = true
		return 0, false
	}
	if record.character == 3 || record.character == 4 {
		return byte(record.character), true
	}
	if record.character == 0 || record.character > 0x7f || alt {
		return 0x80, true
	}
	return byte(record.character), true
}

func adminSetupConsoleModifier(key uint16) bool {
	switch key {
	case 0x10, 0x11, 0x12, 0x14, 0x5b, 0x5c, 0x90, 0x91, 0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5:
		return true
	default:
		return false
	}
}
