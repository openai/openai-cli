//go:build windows

package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Experimental test adapter only. It is never selected by production setup.
// INPUT_RECORD contains a WORD tag, padding, and a 16-byte KEY_EVENT_RECORD.
// https://learn.microsoft.com/en-us/windows/console/input-record-str
// https://learn.microsoft.com/en-us/windows/console/key-event-record-str
type adminConsoleNowaitRecord struct {
	eventType    uint16
	_            uint16
	keyDown      int32
	repeat       uint16
	virtualKey   uint16
	scanCode     uint16
	character    uint16
	controlState uint32
}

const adminConsoleNowaitReaderType = "*custom.adminConsoleNowaitReader"

var adminConsoleReadInputEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputExW")

type adminConsoleNowaitReader struct {
	input        windows.Handle // Borrowed. Never close shared stdin.
	originalMode uint32
	canceled     atomic.Bool
	closed       atomic.Bool
	reading      atomic.Bool
	records      [32]adminConsoleNowaitRecord
	count, next  int
	pending      byte
	repeats      uint32
	altNumpad    bool
}

func newAdminConsoleNowaitReader(input io.Reader) (adminSetupKeyReader, error) {
	file, ok := input.(*os.File)
	if !ok || file.Fd() != os.Stdin.Fd() {
		return nil, errors.New("candidate requires console stdin")
	}
	if err := adminConsoleReadInputEx.Find(); err != nil {
		return nil, err
	}
	reader := &adminConsoleNowaitReader{input: windows.Handle(file.Fd())}
	if err := windows.GetConsoleMode(reader.input, &reader.originalMode); err != nil {
		return nil, err
	}
	// Match the pinned UV active mode to isolate the input API in this experiment.
	mode := uint32(windows.ENABLE_VIRTUAL_TERMINAL_INPUT | windows.ENABLE_WINDOW_INPUT | windows.ENABLE_EXTENDED_FLAGS)
	if err := windows.SetConsoleMode(reader.input, mode); err != nil {
		return nil, err
	}
	if err := reader.discardPendingSnapshot(); err != nil {
		_ = windows.SetConsoleMode(reader.input, reader.originalMode)
		reader.clearInput()
		return nil, errAdminSetupKeyIO
	}
	return reader, nil
}

func (reader *adminConsoleNowaitReader) Read(output []byte) (int, error) {
	if len(output) == 0 {
		return 0, nil
	}
	if !reader.reading.CompareAndSwap(false, true) {
		return 0, errors.New("candidate does not allow concurrent reads")
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
		count, err := adminConsoleNowaitReadRecords(reader.input, reader.records[:])
		if err != nil {
			reader.clearInput()
			return 0, err
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
			return 0, err
		}
		if state != windows.WAIT_OBJECT_0 && state != uint32(windows.WAIT_TIMEOUT) {
			return 0, errors.New("candidate console wait failed")
		}
	}
}

func (reader *adminConsoleNowaitReader) Cancel() bool {
	reader.canceled.Store(true)
	return true // The finite read loop cooperatively observes this request.
}

func (reader *adminConsoleNowaitReader) Close() error {
	reader.canceled.Store(true)
	if reader.reading.Load() {
		return errors.New("candidate close requires completed reads")
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
func (reader *adminConsoleNowaitReader) discardPendingSnapshot() error {
	var remaining uint32
	if windows.GetNumberOfConsoleInputEvents(reader.input, &remaining) != nil {
		return errAdminSetupKeyIO
	}
	var batch [32]adminConsoleNowaitRecord
	defer clear(batch[:])
	for remaining != 0 {
		limit := min(remaining, uint32(len(batch)))
		count, err := adminConsoleNowaitReadRecords(reader.input, batch[:int(limit)])
		clear(batch[:])
		if err != nil || count == 0 {
			return errAdminSetupKeyIO
		}
		remaining -= uint32(count)
	}
	return nil
}

func adminConsoleNowaitReadRecords(input windows.Handle, records []adminConsoleNowaitRecord) (int, error) {
	var count uint32
	// NOWAIT consumes only available records. No pending ReadFile request exists.
	// https://learn.microsoft.com/en-us/windows/console/readconsoleinputex
	result, _, callErr := adminConsoleReadInputEx.Call(uintptr(input), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&count)), 0x0002)
	if result == 0 {
		if callErr != nil && callErr != windows.ERROR_SUCCESS {
			return 0, callErr
		}
		return 0, errors.New("candidate console record read failed")
	}
	if count > uint32(len(records)) {
		return 0, errors.New("candidate received an invalid record count")
	}
	return int(count), nil
}

func (reader *adminConsoleNowaitReader) clearRecords() {
	clear(reader.records[:])
	reader.count, reader.next = 0, 0
}

func (reader *adminConsoleNowaitReader) clearInput() {
	reader.clearRecords()
	reader.pending, reader.repeats = 0, 0
	reader.altNumpad = false
}

func (reader *adminConsoleNowaitReader) copyRecords(output []byte) int {
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
		reader.records[reader.next] = adminConsoleNowaitRecord{}
		reader.next++
		value, emit := reader.recordCharacter(record)
		if emit {
			reader.pending, reader.repeats = value, uint32(max(record.repeat, 1))
		}
	}
	return written
}

// Character contract for this experiment:
// - Preserve translated ASCII, repeats, and character-bearing Alt release.
// - Ignore only non-text records and identified modifier/composition transitions.
// - Emit a rejected non-ASCII byte for unsafe or ambiguous text-bearing input.
// AltGr/layout parity remains unestablished; these fail-closed cases are not a
// proposed shipping keyboard policy. They cannot silently become a valid key.
func (reader *adminConsoleNowaitReader) recordCharacter(record adminConsoleNowaitRecord) (byte, bool) {
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
	if record.character == 0 && adminConsoleNowaitModifier(record.virtualKey) {
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

func adminConsoleNowaitModifier(key uint16) bool {
	switch key {
	case 0x10, 0x11, 0x12, 0x14, 0x5b, 0x5c, 0x90, 0x91, 0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5:
		return true
	default:
		return false
	}
}

// Called inside every candidate observer. It cannot pass merely by compiling.
func adminConsoleNowaitContractChecks() map[string]bool {
	var record adminConsoleNowaitRecord
	checks := map[string]bool{
		"abi_record_20_bytes": unsafe.Sizeof(record) == 20,
		"abi_field_widths":    unsafe.Sizeof(record.eventType) == 2 && unsafe.Sizeof(record.keyDown) == 4 && unsafe.Sizeof(record.repeat) == 2 && unsafe.Sizeof(record.virtualKey) == 2 && unsafe.Sizeof(record.scanCode) == 2 && unsafe.Sizeof(record.character) == 2 && unsafe.Sizeof(record.controlState) == 4,
		"abi_field_offsets":   unsafe.Offsetof(record.eventType) == 0 && unsafe.Offsetof(record.keyDown) == 4 && unsafe.Offsetof(record.repeat) == 8 && unsafe.Offsetof(record.virtualKey) == 10 && unsafe.Offsetof(record.scanCode) == 12 && unsafe.Offsetof(record.character) == 14 && unsafe.Offsetof(record.controlState) == 16,
	}
	key := func(value uint16) adminConsoleNowaitRecord {
		return adminConsoleNowaitRecord{eventType: 1, keyDown: 1, repeat: 1, character: value}
	}
	parse := func(records []adminConsoleNowaitRecord, want string, wantErr error) bool {
		reader := &adminConsoleNowaitReader{count: len(records)}
		copy(reader.records[:], records)
		var encoded []byte
		var buffer [3]byte
		for {
			n := reader.copyRecords(buffer[:])
			if n == 0 {
				break
			}
			encoded = append(encoded, buffer[:n]...)
			clear(buffer[:])
		}
		defer clear(encoded)
		reader.clearInput()
		chunks := make(chan []byte, 1)
		chunks <- encoded
		close(chunks)
		value, err := collectAdminSetupKey(context.Background(), chunks)
		defer clear(value)
		return string(value) == want && errors.Is(err, wantErr)
	}
	checks["ascii_backspace_cr"] = parse([]adminConsoleNowaitRecord{key('a'), key('b'), key(8), key('c'), key('\r')}, "ac", nil)
	allASCII := true
	asciiReader := &adminConsoleNowaitReader{}
	for character := uint16(33); character <= 126; character++ {
		value, emit := asciiReader.recordCharacter(key(character))
		allASCII = allASCII && emit && value == byte(character)
	}
	checks["all_printable_ascii"] = allASCII
	checks["ascii_delete_lf"] = parse([]adminConsoleNowaitRecord{key('a'), key('b'), key(127), key('c'), key('\n')}, "ac", nil)
	checks["ctrl_c"] = parse([]adminConsoleNowaitRecord{key('a'), key(3)}, "", context.Canceled)
	altControl := key(3)
	altControl.controlState = 0x2
	checks["alt_ctrl_c_cancels"] = parse([]adminConsoleNowaitRecord{key('a'), altControl}, "", context.Canceled)
	checks["ctrl_d"] = parse([]adminConsoleNowaitRecord{key('a'), key(4)}, "", context.Canceled)
	modifier := key(0)
	modifier.virtualKey = 0x10
	release := key('a')
	release.keyDown = 0
	checks["modifier_and_key_release"] = parse([]adminConsoleNowaitRecord{modifier, key('a'), release, key('\r')}, "a", nil)
	var modifiers []adminConsoleNowaitRecord
	for _, virtualKey := range []uint16{0x10, 0x11, 0x12, 0x14, 0x5b, 0x5c, 0x90, 0x91, 0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5} {
		record := key(0)
		record.virtualKey = virtualKey
		modifiers = append(modifiers, record)
	}
	modifiers = append(modifiers, key('a'), key('\r'))
	checks["modifier_only_transitions"] = parse(modifiers, "a", nil)
	checks["non_text_record"] = parse([]adminConsoleNowaitRecord{{eventType: 4}, key('a'), key('\r')}, "a", nil)
	composition := key('6')
	composition.virtualKey = 0x66
	composition.controlState = 0x2
	altRelease := key('a')
	altRelease.keyDown = 0
	altRelease.virtualKey = 0x12
	checks["alt_numpad_release"] = parse([]adminConsoleNowaitRecord{composition, altRelease, key('\r')}, "a", nil)
	altNUL := altRelease
	altNUL.character = 0
	checks["alt_numpad_nul_rejected"] = parse([]adminConsoleNowaitRecord{key('a'), composition, altNUL, key('b'), key('\r')}, "", errAdminSetupKeyInvalid)
	composed := &adminConsoleNowaitReader{count: 1}
	composed.records[0] = composition
	var composedOutput [1]byte
	compositionStarted := composed.copyRecords(composedOutput[:]) == 0 && composed.altNumpad
	composed.clearRecords()
	composed.count = 1
	composed.records[0] = altNUL
	checks["alt_numpad_crosses_batches"] = compositionStarted && composed.copyRecords(composedOutput[:]) == 1 && composedOutput[0] == 0x80
	composed.clearInput()
	clear(composedOutput[:])
	checks["surrogate_pair_rejected"] = parse([]adminConsoleNowaitRecord{key('a'), key(0xd83d), key(0xde00), key('b'), key('\r')}, "", errAdminSetupKeyInvalid)
	for name, invalid := range map[string]adminConsoleNowaitRecord{
		"nul_rejected": key(0), "space_rejected": key(' '), "tab_rejected": key(9), "unicode_rejected": key(0xe9), "surrogate_rejected": key(0xd800),
		"navigation_rejected":          {eventType: 1, keyDown: 1, repeat: 1, virtualKey: 0x25},
		"alt_printable_rejected":       {eventType: 1, keyDown: 1, repeat: 1, character: 'b', controlState: 0x2},
		"altgr_unestablished_rejected": {eventType: 1, keyDown: 1, repeat: 1, character: 'b', controlState: 0x9},
	} {
		checks[name] = parse([]adminConsoleNowaitRecord{key('a'), invalid, key('b'), key('\r')}, "", errAdminSetupKeyInvalid)
	}
	var paste []adminConsoleNowaitRecord
	for _, value := range []byte("\x1b[200~ab\x1b[201~\r") {
		paste = append(paste, key(uint16(value)))
	}
	checks["bracketed_paste"] = parse(paste, "ab", nil)
	paste = nil
	for _, value := range []byte("\x1b[200~ab\x03") {
		paste = append(paste, key(uint16(value)))
	}
	checks["incomplete_paste_ctrl_c"] = parse(paste, "", context.Canceled)
	repeated := key('a')
	repeated.repeat = 5
	checks["repeat_staging"] = parse([]adminConsoleNowaitRecord{repeated, key('\r')}, "aaaaa", nil)
	repeated.repeat = 0
	checks["zero_repeat_is_one"] = parse([]adminConsoleNowaitRecord{repeated, key('\r')}, "a", nil)
	repeated.repeat = 65535
	staged := &adminConsoleNowaitReader{count: 1}
	staged.records[0] = repeated
	var small [17]byte
	checks["large_repeat_is_incremental"] = staged.copyRecords(small[:]) == len(small) && staged.repeats == 65535-uint32(len(small))
	staged.altNumpad = true
	staged.clearInput()
	clear(small[:])
	checks["owned_staging_cleared"] = staged.records == [32]adminConsoleNowaitRecord{} && staged.count == 0 && staged.next == 0 && staged.pending == 0 && staged.repeats == 0 && !staged.altNumpad
	return checks
}
