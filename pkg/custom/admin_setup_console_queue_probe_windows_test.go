//go:build windows

package custom

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var adminConsolePeekInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekConsoleInputW")

type adminConsoleFixtureInjection struct {
	RawStorage         bool   `json:"raw_storage"`
	InjectionMode      uint32 `json:"injection_mode"`
	EchoOff            bool   `json:"echo_off"`
	ActiveModeRestored bool   `json:"active_mode_restored"`
}

// The raw controls change only write-time VT translation. Both readers receive
// their exact original active mode before the observer peeks or starts Read.
func adminConsoleWriteParityRecords(records []adminSetupConsoleInputRecord, raw bool, active uint32) (written uint32, evidence adminConsoleFixtureInjection, err error) {
	evidence.RawStorage, evidence.InjectionMode = raw, active
	input, handleErr := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if handleErr != nil {
		return 0, evidence, errors.New("fixture input handle unavailable")
	}
	var before uint32
	if windows.GetConsoleMode(input, &before) != nil || before != active {
		return 0, evidence, errors.New("fixture active mode changed before injection")
	}
	if raw {
		evidence.InjectionMode &^= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	}
	evidence.EchoOff = evidence.InjectionMode&windows.ENABLE_ECHO_INPUT == 0
	if !evidence.EchoOff {
		return 0, evidence, errors.New("fixture injection requires hidden input")
	}
	defer func() {
		if raw && windows.SetConsoleMode(input, active) != nil {
			err = errors.New("fixture could not restore active mode")
		}
		var restored uint32
		evidence.ActiveModeRestored = windows.GetConsoleMode(input, &restored) == nil && restored == active
		if !evidence.ActiveModeRestored {
			err = errors.New("fixture active mode restoration failed")
		}
	}()
	if raw && windows.SetConsoleMode(input, evidence.InjectionMode) != nil {
		return 0, evidence, errors.New("fixture could not select raw storage")
	}
	var during uint32
	if windows.GetConsoleMode(input, &during) != nil || during != evidence.InjectionMode {
		return 0, evidence, errors.New("fixture injection mode was not established")
	}
	evidence.EchoOff = during&windows.ENABLE_ECHO_INPUT == 0
	written, err = adminConsoleWriteNativeRecords(records)
	return written, evidence, err
}

type adminConsoleFixturePeek struct {
	PendingBefore          uint32            `json:"pending_before"`
	Peeked                 uint32            `json:"peeked"`
	PendingAfter           uint32            `json:"pending_after"`
	CountBeforeSucceeded   bool              `json:"count_before_succeeded"`
	CountAfterSucceeded    bool              `json:"count_after_succeeded"`
	PeekSucceeded          bool              `json:"peek_succeeded"`
	Complete               bool              `json:"complete"`
	RecordsMatchFixture    bool              `json:"records_match_fixture"`
	KeyDownRecords         uint32            `json:"key_down_records"`
	CharacterKeyUpRecords  uint32            `json:"character_key_up_records"`
	RepeatTotal            uint64            `json:"repeat_total"`
	MaxRepeat              uint16            `json:"max_repeat"`
	Repeat37Records        uint32            `json:"repeat_37_records"`
	AltReleaseARecords     uint32            `json:"alt_release_a_records"`
	ZeroCharacterAltDigits uint32            `json:"zero_character_alt_digits"`
	Categories             map[string]uint32 `json:"categories"`
}

// This reads metadata before any reader starts. It never consumes, reinserts,
// prints, or retains fixture contents. The fixed bound applies only to tests.
func adminConsolePeekFixture(expected []adminSetupConsoleInputRecord) adminConsoleFixturePeek {
	evidence := adminConsoleFixturePeek{Categories: map[string]uint32{}}
	input, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil || adminConsolePeekInput.Find() != nil {
		return evidence
	}
	evidence.CountBeforeSucceeded = windows.GetNumberOfConsoleInputEvents(input, &evidence.PendingBefore) == nil
	var records [256]adminSetupConsoleInputRecord
	defer clear(records[:])
	if !evidence.CountBeforeSucceeded || evidence.PendingBefore == 0 || evidence.PendingBefore > uint32(len(records)) {
		return evidence
	}
	result, _, _ := adminConsolePeekInput.Call(uintptr(input), uintptr(unsafe.Pointer(&records[0])), uintptr(evidence.PendingBefore), uintptr(unsafe.Pointer(&evidence.Peeked)))
	runtime.KeepAlive(records)
	evidence.PeekSucceeded = result != 0 && evidence.Peeked <= uint32(len(records))
	evidence.CountAfterSucceeded = windows.GetNumberOfConsoleInputEvents(input, &evidence.PendingAfter) == nil
	evidence.Complete = evidence.PeekSucceeded && evidence.Peeked == evidence.PendingBefore && evidence.CountAfterSucceeded && evidence.PendingAfter == evidence.PendingBefore
	if !evidence.Complete {
		return evidence
	}
	evidence.RecordsMatchFixture = int(evidence.Peeked) == len(expected)
	for i, record := range records[:evidence.Peeked] {
		if i >= len(expected) || record != expected[i] {
			evidence.RecordsMatchFixture = false
		}
		if record.eventType != 1 {
			evidence.Categories["non-key"]++
			continue
		}
		evidence.RepeatTotal += uint64(record.repeat)
		evidence.MaxRepeat = max(evidence.MaxRepeat, record.repeat)
		if record.repeat == 37 {
			evidence.Repeat37Records++
		}
		if record.keyDown != 0 {
			evidence.KeyDownRecords++
			if record.character == 0 && record.virtualKey >= 0x60 && record.virtualKey <= 0x69 && record.controlState&0x0003 != 0 {
				evidence.ZeroCharacterAltDigits++
			}
		} else if record.character != 0 {
			evidence.CharacterKeyUpRecords++
			if record.virtualKey == 0x12 && record.character == 'A' {
				evidence.AltReleaseARecords++
			}
		}
		category := "non-ASCII"
		switch {
		case record.character == 0:
			category = "NUL"
		case record.character <= 0x7f:
			category = adminConsoleByteCategory(byte(record.character))
		case record.character >= 0xd800 && record.character <= 0xdfff:
			category = "surrogate"
		}
		evidence.Categories[category]++
	}
	return evidence
}
