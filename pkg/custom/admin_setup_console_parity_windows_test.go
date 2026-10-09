//go:build windows

package custom

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	uv "github.com/charmbracelet/ultraviolet"
	"golang.org/x/sys/windows"
)

var adminConsoleWriteInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("WriteConsoleInputW")

// Native fixture writer shared by parity and queue tests. The count describes
// records accepted by Windows, not bytes delivered to a particular reader.
func adminConsoleWriteNativeRecords(records []adminConsoleNowaitRecord) (uint32, error) {
	if len(records) == 0 {
		return 0, nil
	}
	if unsafe.Sizeof(records[0]) != 20 || uint64(len(records)) > uint64(^uint32(0)) {
		return 0, errors.New("native fixture record layout or count is invalid")
	}
	if err := adminConsoleWriteInput.Find(); err != nil {
		return 0, err
	}
	input, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil {
		return 0, err
	}
	var written uint32
	result, _, callErr := adminConsoleWriteInput.Call(uintptr(input), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&written)))
	runtime.KeepAlive(records)
	if result == 0 {
		if callErr != nil && callErr != windows.ERROR_SUCCESS {
			return written, callErr
		}
		return written, errors.New("native fixture record write failed")
	}
	if written > uint32(len(records)) {
		return written, errors.New("native fixture returned an invalid write count")
	}
	return written, nil
}

type adminConsoleParityInput struct {
	class      string
	records    []adminConsoleNowaitRecord
	rawStorage bool
}

// These are synthetic native records. Their classes describe fixture intent,
// not an assumed outcome: the controller compares each actual UV result.
func adminConsoleParityFixture(name string) (adminConsoleParityInput, bool) {
	key := func(character uint16) adminConsoleNowaitRecord {
		return adminConsoleNowaitRecord{eventType: 1, keyDown: 1, repeat: 1, character: character}
	}
	text := func(value string) []adminConsoleNowaitRecord {
		records := make([]adminConsoleNowaitRecord, 0, len(value))
		for _, character := range value {
			records = append(records, key(uint16(character)))
		}
		return records
	}
	const prefix = "sk-admin-SYNTHETIC-parity-"
	with := func(extra ...adminConsoleNowaitRecord) []adminConsoleNowaitRecord {
		records := append(text(prefix), extra...)
		return append(records, key('\r'))
	}
	fixture := adminConsoleParityInput{class: "rejected-input"}
	switch name {
	case "printable_boundary":
		fixture.class = "accepted-input"
		fixture.records = text(prefix)
		for character := uint16(33); character <= 126; character++ {
			fixture.records = append(fixture.records, key(character))
		}
		fixture.records = append(fixture.records, key('\r'))
	case "repeat_boundary", "repeat_raw_storage":
		fixture.class = "accepted-input"
		fixture.rawStorage = name == "repeat_raw_storage"
		fixture.records = text(prefix + "0123456789abcdef0123456789abcdef")
		repeated := key('a')
		repeated.repeat = 37
		fixture.records = append(fixture.records, repeated, key('\r'))
	case "backspace_delete":
		fixture.class = "accepted-input"
		fixture.records = with(key('a'), key('b'), key(8), key('c'), key(127), key('d'))
	case "ctrl_c", "ctrl_d":
		fixture.class = "cancel-input"
		character := uint16(3)
		if name == "ctrl_d" {
			character = 4
		}
		fixture.records = append(text(prefix), key(character))
	case "bracketed_paste":
		fixture.class = "accepted-input"
		fixture.records = text("\x1b[200~" + prefix + "paste\x1b[201~\r")
	case "invalid_paste_space":
		fixture.records = text("\x1b[200~" + prefix + " space\x1b[201~\r")
	case "invalid_paste_nested":
		fixture.records = text("\x1b[200~" + prefix + "\x1b[200~nested\x1b[201~\x1b[201~\r")
	case "incomplete_paste_ctrl_c":
		fixture.class = "cancel-input"
		fixture.records = text("\x1b[200~" + prefix + "\x03")
	case "nul":
		fixture.records = with(key(0), key('z'))
	case "space":
		fixture.records = with(key(' '), key('z'))
	case "tab":
		fixture.records = with(key('\t'), key('z'))
	case "unicode":
		fixture.records = with(key(0x00e9), key('z'))
	case "surrogate_pair":
		fixture.records = with(key(0xd83d), key(0xde00), key('z'))
	case "lone_surrogate":
		fixture.class = "unsupported-record"
		fixture.records = with(key(0xd800), key('z'))
	case "modifier_transitions":
		fixture.class = "accepted-input"
		fixture.records = text(prefix)
		for _, virtualKey := range []uint16{0x10, 0x11, 0x12} {
			down := key(0)
			down.virtualKey = virtualKey
			switch virtualKey {
			case 0x10:
				down.controlState = 0x0010
			case 0x11:
				down.controlState = 0x0008
			case 0x12:
				down.controlState = 0x0002
			}
			up := down
			up.keyDown, up.controlState = 0, 0
			fixture.records = append(fixture.records, down, up)
		}
		release := key('z')
		release.keyDown, release.virtualKey = 0, 'Z'
		fixture.records = append(fixture.records, key('a'), release, key('\r'))
	case "navigation":
		navigation := key(0)
		navigation.virtualKey, navigation.controlState = 0x25, 0x0100
		fixture.records = with(navigation, key('z'))
	case "alt_ascii":
		alt := key('b')
		alt.virtualKey, alt.controlState = 'B', 0x0002
		fixture.records = with(alt, key('z'))
	case "altgr_translated_ascii":
		fixture.class = "accepted-input"
		// Already translated ASCII has no remaining physical modifier flags.
		fixture.records = with(key('@'), key('z'))
	case "altgr_physical_ascii":
		fixture.class = "accepted-input"
		altgr := key('@')
		altgr.virtualKey, altgr.controlState = 'Q', 0x0009
		fixture.records = with(altgr, key('z'))
	case "alt_numpad_release", "malformed_alt_release", "alt_numpad_raw_storage":
		fixture.class = "accepted-input"
		digit := key('6')
		digit.virtualKey, digit.controlState = 0x66, 0x0022
		lastDigit := digit
		lastDigit.character, lastDigit.virtualKey = '5', 0x65
		if name == "alt_numpad_raw_storage" {
			fixture.rawStorage = true
			digit.character, lastDigit.character = 0, 0
		}
		release := key('A')
		release.keyDown, release.virtualKey, release.controlState = 0, 0x12, 0x0020
		if name == "malformed_alt_release" {
			fixture.class, release.character = "unsupported-record", 0
		}
		fixture.records = with(digit, lastDigit, release, key('z'))
	default:
		return adminConsoleParityInput{}, false
	}
	return fixture, true
}

func adminConsoleParityOutcome(err error, timedOut bool) string {
	if timedOut {
		return "timeout"
	}
	switch {
	case err == nil:
		return "accepted"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, errAdminSetupKeyEmpty):
		return "empty"
	case errors.Is(err, errAdminSetupKeyInvalid):
		return "invalid"
	case errors.Is(err, errAdminSetupKeyIO):
		return "io"
	default:
		return "other"
	}
}

// Each process observes one fixture and one reader in its own fresh ConPTY.
// This intentionally does not use helper-only conversion as parity evidence.
func TestAdminSetupConsoleParityChild(t *testing.T) {
	address := os.Getenv("OPENAI_ADMIN_CONSOLE_CONTROL")
	if address == "" {
		t.Skip("requires the owned Windows console controller")
	}
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		t.Fatal("parity observer control must use a loopback address")
	}
	nonce := os.Getenv("OPENAI_ADMIN_CONSOLE_NONCE")
	if len(nonce) < 32 {
		t.Fatal("parity observer requires a controller nonce")
	}
	name := os.Getenv("OPENAI_ADMIN_CONSOLE_PARITY")
	fixture, known := adminConsoleParityFixture(name)
	if !known {
		t.Fatal("parity observer received an unsupported fixture")
	}
	defer clear(fixture.records)
	mode := os.Getenv("OPENAI_ADMIN_CONSOLE_READER")
	expectedReader := "*uv.conInputReader"
	if mode == "console-nowait" {
		expectedReader = adminConsoleNowaitReaderType
	} else if mode != "uv" {
		t.Fatal("parity observer received an unsupported reader")
	}
	original, err := adminConsoleGetModes()
	if err != nil {
		adminConsoleLogHandleFailure(t, "parity original")
		t.Fatal("parity observer requires real console standard handles")
	}
	connection, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal("parity observer could not connect")
	}
	defer connection.Close()
	if connection.SetDeadline(time.Now().Add(15*time.Second)) != nil {
		t.Fatal("parity observer could not bound its control channel")
	}
	observer := &adminConsoleObserver{encoder: json.NewEncoder(connection)}
	observer.send(map[string]any{"event": "hello", "nonce": nonce, "original": original, "scenario": "parity/" + name, "reader_mode": mode})
	decoder := json.NewDecoder(io.LimitReader(connection, 64<<10))
	var start adminConsoleControl
	if decoder.Decode(&start) != nil || start.Action != "start" || start.Nonce != nonce {
		t.Fatal("parity observer authentication failed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var reader *adminConsoleObservedReader
	var active adminConsoleModes
	var written uint32
	var writeSucceeded bool
	var peek adminConsoleFixturePeek
	rawStorageProved := !fixture.rawStorage
	key, readErr := readAdminSetupKeyWithReader(ctx, os.Stdin, os.Stderr, func(input io.Reader) (adminSetupKeyReader, error) {
		var actual adminSetupKeyReader
		var createErr error
		if mode == "uv" {
			actual, createErr = uv.NewCancelReader(input)
		} else {
			actual, createErr = newAdminConsoleNowaitReader(input)
		}
		if createErr != nil {
			return nil, createErr
		}
		reader = &adminConsoleObservedReader{
			adminSetupKeyReader: actual, observer: observer, test: t, phase: name, readerType: fmt.Sprintf("%T", actual),
			lastReadError: "not-read", firstByteCategory: "none", lastByteCategory: "none",
			byteCategories: map[string]int{"CR": 0, "LF": 0, "ctrl-C": 0, "ctrl-D": 0, "ascii-printable": 0, "ESC": 0, "other": 0},
		}
		var modeErr error
		active, modeErr = adminConsoleGetModes()
		if modeErr != nil || active.Stdin&(windows.ENABLE_ECHO_INPUT|windows.ENABLE_LINE_INPUT) != 0 || active.Stdin&windows.ENABLE_VIRTUAL_TERMINAL_INPUT == 0 {
			reader.Close()
			return nil, errors.New("parity reader did not establish hidden VT input")
		}
		ready := reader.frame("ready")
		ready["original"], ready["active"] = original, active
		observer.send(ready)
		// Both constructors have finished their startup discard before injection.
		// The native call acknowledges the complete write before Read starts.
		var writeErr error
		var injection adminConsoleFixtureInjection
		written, injection, writeErr = adminConsoleWriteParityRecords(fixture.records, fixture.rawStorage, active.Stdin)
		writeSucceeded = writeErr == nil && written == uint32(len(fixture.records))
		observer.send(map[string]any{"event": "parity_write", "phase": name, "records_requested": len(fixture.records), "records_written": written, "write_succeeded": writeSucceeded, "injection": injection})
		if !writeSucceeded {
			reader.Close()
			return nil, errors.New("parity native fixture write was incomplete")
		}
		peek = adminConsolePeekFixture(fixture.records)
		if fixture.rawStorage {
			rawStorageProved = peek.Complete && peek.RecordsMatchFixture
			if name == "repeat_raw_storage" {
				rawStorageProved = rawStorageProved && peek.Repeat37Records == 1 && peek.MaxRepeat == 37
			} else {
				rawStorageProved = rawStorageProved && peek.AltReleaseARecords == 1 && peek.ZeroCharacterAltDigits == 2
			}
		}
		observer.send(map[string]any{"event": "parity_peek", "phase": name, "peek": peek, "raw_storage_proved": rawStorageProved})
		if !peek.Complete || !rawStorageProved {
			reader.Close()
			return nil, errors.New("parity native queue did not preserve its required fixture")
		}
		return reader, nil
	})
	// Observe the completed call before test cleanup can cancel its context.
	contextErr := ctx.Err()
	timedOut := errors.Is(contextErr, context.DeadlineExceeded)
	if reader == nil {
		clear(key)
		t.Fatal("parity observer did not construct its reader")
	}
	frame := reader.frame("parity_complete")
	restored, restoreErr := adminConsoleGetModes()
	acceptedDigest, acceptedLength := "", 0
	if readErr == nil && !timedOut {
		// Input is generated only by the fixed fixture catalog above.
		digest := sha256.Sum256(key)
		acceptedDigest, acceptedLength = fmt.Sprintf("%x", digest), len(key)
	}
	keyEmpty := len(key) == 0
	clear(key)
	cancel()
	readsComplete := frame["started"] == frame["completed"] && frame["active_reads"] == 0
	predicates := map[string]bool{
		"fixture_peeked": peek.Complete, "raw_storage_proved": rawStorageProved,
		"observer_healthy": observer.healthy(), "fixture_written": writeSucceeded, "deadline_not_exceeded": !timedOut,
		"reader_is_native": reader.readerType == expectedReader, "read_started": frame["started"].(int) > 0,
		"reads_complete": readsComplete, "cancel_called_once": frame["cancel_calls"] == 1,
		"close_called_once": frame["close_calls"] == 1, "reads_complete_at_close": frame["reads_complete_at_close"] == true,
		"close_succeeded":   frame["close_succeeded"] == true,
		"restore_succeeded": restoreErr == nil, "restored_modes_match": restored == original,
		"echo_disabled":       active.Stdin&windows.ENABLE_ECHO_INPUT == 0,
		"line_input_disabled": active.Stdin&windows.ENABLE_LINE_INPUT == 0,
		"vt_input_enabled":    active.Stdin&windows.ENABLE_VIRTUAL_TERMINAL_INPUT != 0,
	}
	passed := true
	for _, valid := range predicates {
		passed = passed && valid
	}
	frame["original"], frame["active"], frame["restored"] = original, active, restored
	frame["scenario"], frame["reader_mode"], frame["fixture_class"] = "parity/"+name, mode, fixture.class
	frame["parser_outcome"], frame["read_error_category"] = adminConsoleParityOutcome(readErr, timedOut), adminConsoleKeyErrorCategory(readErr)
	frame["accepted_key_sha256"], frame["accepted_key_length"], frame["key_empty"] = acceptedDigest, acceptedLength, keyEmpty
	frame["timed_out"], frame["context_canceled_before_cleanup"] = timedOut, contextErr != nil
	frame["context_canceled"], frame["reads_complete"] = errors.Is(readErr, context.Canceled), readsComplete
	frame["records_requested"], frame["records_written"], frame["write_succeeded"] = len(fixture.records), written, writeSucceeded
	frame["predicates"], frame["passed"] = predicates, passed
	frame["restored_modes_match"], frame["observer_healthy"] = restored == original, observer.healthy()
	observer.send(frame)
	var ack adminConsoleControl
	if decoder.Decode(&ack) != nil || ack.Action != "ack" || !observer.healthy() {
		t.Fatal("parity observer did not receive its final acknowledgement")
	}
	if !passed {
		t.Fatal("parity observer did not complete its fixture and lifecycle contract")
	}
}
