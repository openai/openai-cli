//go:build windows

package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const adminSetupConsoleReaderType = "*custom.adminSetupConsoleReader"

// Native observers execute these checks against the production record converter.
func adminSetupConsoleReaderContractChecks() map[string]bool {
	var record adminSetupConsoleInputRecord
	checks := map[string]bool{
		"abi_record_20_bytes": unsafe.Sizeof(record) == 20,
		"abi_field_widths":    unsafe.Sizeof(record.eventType) == 2 && unsafe.Sizeof(record.keyDown) == 4 && unsafe.Sizeof(record.repeat) == 2 && unsafe.Sizeof(record.virtualKey) == 2 && unsafe.Sizeof(record.scanCode) == 2 && unsafe.Sizeof(record.character) == 2 && unsafe.Sizeof(record.controlState) == 4,
		"abi_field_offsets":   unsafe.Offsetof(record.eventType) == 0 && unsafe.Offsetof(record.keyDown) == 4 && unsafe.Offsetof(record.repeat) == 8 && unsafe.Offsetof(record.virtualKey) == 10 && unsafe.Offsetof(record.scanCode) == 12 && unsafe.Offsetof(record.character) == 14 && unsafe.Offsetof(record.controlState) == 16,
	}
	key := func(value uint16) adminSetupConsoleInputRecord {
		return adminSetupConsoleInputRecord{eventType: 1, keyDown: 1, repeat: 1, character: value}
	}
	parse := func(records []adminSetupConsoleInputRecord, want string, wantErr error) bool {
		reader := &adminSetupConsoleReader{count: len(records)}
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
	checks["ascii_backspace_cr"] = parse([]adminSetupConsoleInputRecord{key('a'), key('b'), key(8), key('c'), key('\r')}, "ac", nil)
	allASCII := true
	asciiReader := &adminSetupConsoleReader{}
	for character := uint16(33); character <= 126; character++ {
		value, emit := asciiReader.recordCharacter(key(character))
		allASCII = allASCII && emit && value == byte(character)
	}
	checks["all_printable_ascii"] = allASCII
	checks["ascii_delete_lf"] = parse([]adminSetupConsoleInputRecord{key('a'), key('b'), key(127), key('c'), key('\n')}, "ac", nil)
	checks["ctrl_c"] = parse([]adminSetupConsoleInputRecord{key('a'), key(3)}, "", context.Canceled)
	altControl := key(3)
	altControl.controlState = 0x2
	checks["alt_ctrl_c_cancels"] = parse([]adminSetupConsoleInputRecord{key('a'), altControl}, "", context.Canceled)
	checks["ctrl_d"] = parse([]adminSetupConsoleInputRecord{key('a'), key(4)}, "", context.Canceled)
	modifier := key(0)
	modifier.virtualKey = 0x10
	release := key('a')
	release.keyDown = 0
	checks["modifier_and_key_release"] = parse([]adminSetupConsoleInputRecord{modifier, key('a'), release, key('\r')}, "a", nil)
	var modifiers []adminSetupConsoleInputRecord
	for _, virtualKey := range []uint16{0x10, 0x11, 0x12, 0x14, 0x5b, 0x5c, 0x90, 0x91, 0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5} {
		record := key(0)
		record.virtualKey = virtualKey
		modifiers = append(modifiers, record)
	}
	modifiers = append(modifiers, key('a'), key('\r'))
	checks["modifier_only_transitions"] = parse(modifiers, "a", nil)
	checks["non_text_record"] = parse([]adminSetupConsoleInputRecord{{eventType: 4}, key('a'), key('\r')}, "a", nil)
	composition := key('6')
	composition.virtualKey = 0x66
	composition.controlState = 0x2
	altRelease := key('a')
	altRelease.keyDown = 0
	altRelease.virtualKey = 0x12
	checks["alt_numpad_release"] = parse([]adminSetupConsoleInputRecord{composition, altRelease, key('\r')}, "a", nil)
	altNUL := altRelease
	altNUL.character = 0
	checks["alt_numpad_nul_rejected"] = parse([]adminSetupConsoleInputRecord{key('a'), composition, altNUL, key('b'), key('\r')}, "", errAdminSetupKeyInvalid)
	composed := &adminSetupConsoleReader{count: 1}
	composed.records[0] = composition
	var composedOutput [1]byte
	compositionStarted := composed.copyRecords(composedOutput[:]) == 0 && composed.altNumpad
	composed.clearRecords()
	composed.count = 1
	composed.records[0] = altNUL
	checks["alt_numpad_crosses_batches"] = compositionStarted && composed.copyRecords(composedOutput[:]) == 1 && composedOutput[0] == 0x80
	composed.clearInput()
	clear(composedOutput[:])
	checks["surrogate_pair_rejected"] = parse([]adminSetupConsoleInputRecord{key('a'), key(0xd83d), key(0xde00), key('b'), key('\r')}, "", errAdminSetupKeyInvalid)
	for name, invalid := range map[string]adminSetupConsoleInputRecord{
		"nul_rejected": key(0), "space_rejected": key(' '), "tab_rejected": key(9), "unicode_rejected": key(0xe9), "surrogate_rejected": key(0xd800),
		"navigation_rejected":          {eventType: 1, keyDown: 1, repeat: 1, virtualKey: 0x25},
		"alt_printable_rejected":       {eventType: 1, keyDown: 1, repeat: 1, character: 'b', controlState: 0x2},
		"altgr_unestablished_rejected": {eventType: 1, keyDown: 1, repeat: 1, character: 'b', controlState: 0x9},
	} {
		checks[name] = parse([]adminSetupConsoleInputRecord{key('a'), invalid, key('b'), key('\r')}, "", errAdminSetupKeyInvalid)
	}
	var paste []adminSetupConsoleInputRecord
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
	checks["repeat_staging"] = parse([]adminSetupConsoleInputRecord{repeated, key('\r')}, "aaaaa", nil)
	repeated.repeat = 0
	checks["zero_repeat_is_one"] = parse([]adminSetupConsoleInputRecord{repeated, key('\r')}, "a", nil)
	repeated.repeat = 65535
	staged := &adminSetupConsoleReader{count: 1}
	staged.records[0] = repeated
	var small [17]byte
	checks["large_repeat_is_incremental"] = staged.copyRecords(small[:]) == len(small) && staged.repeats == 65535-uint32(len(small))
	staged.altNumpad = true
	staged.clearInput()
	clear(small[:])
	checks["owned_staging_cleared"] = staged.records == [32]adminSetupConsoleInputRecord{} && staged.count == 0 && staged.next == 0 && staged.pending == 0 && staged.repeats == 0 && !staged.altNumpad
	for name, passed := range adminSetupConsoleReaderErrorChecks() {
		checks[name] = passed
	}
	return checks
}

// These checks own their pipe handles and never change shared console handles.
// Native observers execute them; cross-compilation alone is not a pass.
func adminSetupConsoleReaderErrorChecks() map[string]bool {
	rejects := func(input io.Reader) bool {
		reader, err := newAdminSetupKeyReader(input)
		if reader != nil {
			_ = reader.Close()
		}
		return reader == nil && err == errAdminSetupKeyIO
	}
	var absent *os.File
	checks := map[string]bool{
		"constructor_nil_rejected":          rejects(nil),
		"constructor_typed_nil_rejected":    rejects(absent),
		"constructor_non_file_rejected":     rejects(strings.NewReader("synthetic")),
		"constructor_non_console_rejected":  false,
		"canceled_read_clears_staging":      false,
		"invalid_handle_read_opaque_error":  false,
		"invalid_handle_close_opaque_error": false,
	}
	if input, output, err := os.Pipe(); err == nil {
		rejected := rejects(input)
		inputErr, outputErr := input.Close(), output.Close()
		checks["constructor_non_console_rejected"] = rejected && inputErr == nil && outputErr == nil
	}
	seed := func() *adminSetupConsoleReader {
		reader := &adminSetupConsoleReader{input: windows.InvalidHandle, count: 1, pending: 'x', repeats: 3, altNumpad: true}
		reader.records[0] = adminSetupConsoleInputRecord{eventType: 1, keyDown: 1, repeat: 1, character: 'x'}
		return reader
	}
	cleared := func(reader *adminSetupConsoleReader) bool {
		return reader.records == [32]adminSetupConsoleInputRecord{} && reader.count == 0 && reader.next == 0 &&
			reader.pending == 0 && reader.repeats == 0 && !reader.altNumpad && !reader.reading.Load()
	}
	var buffer [8]byte
	defer clear(buffer[:])
	canceled := seed()
	requested := canceled.Cancel()
	n, err := canceled.Read(buffer[:])
	checks["canceled_read_clears_staging"] = requested && n == 0 && err == context.Canceled && cleared(canceled)
	if adminSetupReadConsoleInputEx.Find() == nil {
		invalid := &adminSetupConsoleReader{input: windows.InvalidHandle}
		n, err := invalid.Read(buffer[:])
		checks["invalid_handle_read_opaque_error"] = n == 0 && err == errAdminSetupKeyIO && cleared(invalid)
	}
	invalidClose := seed()
	err = invalidClose.Close()
	checks["invalid_handle_close_opaque_error"] = err == errAdminSetupKeyIO && cleared(invalidClose)
	return checks
}
