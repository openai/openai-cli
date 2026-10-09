//go:build windows

package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type adminConsoleModes struct {
	Stdin  uint32 `json:"stdin"`
	Stdout uint32 `json:"stdout"`
	Stderr uint32 `json:"stderr"`
}

func adminConsoleGetModes() (adminConsoleModes, error) {
	var modes adminConsoleModes
	for _, item := range []struct {
		file *os.File
		mode *uint32
	}{{os.Stdin, &modes.Stdin}, {os.Stdout, &modes.Stdout}, {os.Stderr, &modes.Stderr}} {
		if err := windows.GetConsoleMode(windows.Handle(item.file.Fd()), item.mode); err != nil {
			return modes, errors.New("observer requires console standard handles")
		}
	}
	return modes, nil
}

func adminConsoleProbeError(err error) map[string]any {
	result := map[string]any{"failed": err != nil}
	if err != nil {
		var code syscall.Errno
		if errors.As(err, &code) {
			result["windows_error_code"] = uint64(code)
		} else {
			result["unclassified_error"] = true
		}
	}
	return result
}

// Failure-only metadata never contains handle values, paths, or input bytes.
func adminConsoleLogHandleFailure(t *testing.T, stage string) {
	t.Helper()
	var handles []map[string]any
	for _, item := range []struct {
		name     string
		file     *os.File
		standard uint32
	}{
		{"stdin", os.Stdin, windows.STD_INPUT_HANDLE},
		{"stdout", os.Stdout, windows.STD_OUTPUT_HANDLE},
		{"stderr", os.Stderr, windows.STD_ERROR_HANDLE},
	} {
		handle := windows.Handle(item.file.Fd())
		fileType, fileTypeErr := windows.GetFileType(handle)
		var mode uint32
		modeErr := windows.GetConsoleMode(handle, &mode)
		standard, standardErr := windows.GetStdHandle(item.standard)
		handles = append(handles, map[string]any{
			"name": item.name, "file_type": fileType, "get_file_type_error": adminConsoleProbeError(fileTypeErr),
			"console_mode": mode, "get_console_mode_error": adminConsoleProbeError(modeErr),
			"get_std_handle_error":      adminConsoleProbeError(standardErr),
			"go_handle_equals_standard": standardErr == nil && handle == standard,
			"go_handle_null":            handle == 0, "go_handle_invalid": handle == windows.InvalidHandle,
			"standard_handle_null": standard == 0, "standard_handle_invalid": standard == windows.InvalidHandle,
		})
	}
	encoded, err := json.Marshal(map[string]any{"stage": stage, "handles": handles})
	if err != nil {
		t.Error("could not encode console handle diagnostics")
		return
	}
	t.Logf("console handle diagnostics: %s", encoded)
}

type adminConsoleControl struct {
	Action string `json:"action"`
	Phase  string `json:"phase"`
	Nonce  string `json:"nonce"`
}

type adminConsoleObserver struct {
	mu       sync.Mutex
	encoder  *json.Encoder
	failed   bool
	controls chan adminConsoleControl
}

func (observer *adminConsoleObserver) send(frame map[string]any) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.encoder.Encode(frame) != nil {
		observer.failed = true
	}
}

func (observer *adminConsoleObserver) healthy() bool {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return !observer.failed
}

type adminConsoleObservedReader struct {
	adminSetupKeyReader
	mu                sync.Mutex
	observer          *adminConsoleObserver
	test              *testing.T
	phase             string
	readerType        string
	started           int
	completed         int
	active            int
	readBytes         int
	lastReadBytes     int
	lastReadError     string
	firstByteCategory string
	lastByteCategory  string
	byteCategories    map[string]int
	cancelCalls       int
	cancelReturned    *bool
	closeCalls        int
	closeStarted      int
	closeCompleted    int
	closeActive       int
	closeComplete     bool
	closeSucceeded    *bool
	queueAtClose      bool
	queueEvidence     *adminConsoleQueueEvidence
}

func (reader *adminConsoleObservedReader) frame(event string) map[string]any {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	frame := map[string]any{
		"event": event, "phase": reader.phase, "reader_type": reader.readerType,
		"started": reader.started, "completed": reader.completed,
		"active_reads": reader.active, "read_bytes": reader.readBytes, "cancel_calls": reader.cancelCalls,
		"close_calls":     reader.closeCalls,
		"last_read_bytes": reader.lastReadBytes, "last_read_error": reader.lastReadError,
		"first_byte_category": reader.firstByteCategory, "last_byte_category": reader.lastByteCategory,
	}
	categories := make(map[string]int, len(reader.byteCategories))
	for category, count := range reader.byteCategories {
		categories[category] = count
	}
	frame["byte_categories"] = categories
	if reader.cancelReturned != nil {
		frame["cancel_returned"] = *reader.cancelReturned
	}
	if reader.closeCalls > 0 {
		frame["close_started_reads"] = reader.closeStarted
		frame["close_completed_reads"] = reader.closeCompleted
		frame["close_active_reads"] = reader.closeActive
		frame["reads_complete_at_close"] = reader.closeComplete
	}
	if reader.closeSucceeded != nil {
		frame["close_succeeded"] = *reader.closeSucceeded
	}
	if reader.queueEvidence != nil {
		frame["queue"] = reader.queueEvidence
	}
	return frame
}

func (reader *adminConsoleObservedReader) Read(buffer []byte) (int, error) {
	reader.mu.Lock()
	reader.started++
	reader.active++
	reader.mu.Unlock()
	reader.observer.send(reader.frame("read_started"))
	n, err := reader.adminSetupKeyReader.Read(buffer)
	reader.mu.Lock()
	reader.completed++
	reader.active--
	reader.readBytes += n
	reader.lastReadBytes, reader.lastReadError = n, adminConsoleReadErrorCategory(err)
	for _, value := range buffer[:n] {
		category := adminConsoleByteCategory(value)
		if reader.firstByteCategory == "none" {
			reader.firstByteCategory = category
		}
		reader.lastByteCategory = category
		reader.byteCategories[category]++
	}
	reader.mu.Unlock()
	reader.observer.send(reader.frame("read_completed"))
	return n, err
}

// Classify input without retaining any byte values or credential fragments.
func adminConsoleByteCategory(value byte) string {
	switch value {
	case '\r':
		return "CR"
	case '\n':
		return "LF"
	case 3:
		return "ctrl-C"
	case 4:
		return "ctrl-D"
	case 0x1b:
		return "ESC"
	default:
		if value >= 0x20 && value <= 0x7e {
			return "ascii-printable"
		}
		return "other"
	}
}

func adminConsoleReadErrorCategory(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, context.Canceled):
		return "context.Canceled"
	case errors.Is(err, windows.ERROR_OPERATION_ABORTED):
		return "windows-operation-aborted"
	case errors.Is(err, io.EOF):
		return "EOF"
	default:
		return "other"
	}
}

func adminConsoleKeyErrorCategory(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, context.Canceled):
		return "context.Canceled"
	case errors.Is(err, errAdminSetupKeyEmpty):
		return "errAdminSetupKeyEmpty"
	case errors.Is(err, errAdminSetupKeyInvalid):
		return "errAdminSetupKeyInvalid"
	case errors.Is(err, errAdminSetupKeyIO):
		return "errAdminSetupKeyIO"
	default:
		return "other"
	}
}

func (reader *adminConsoleObservedReader) Cancel() bool {
	result := reader.adminSetupKeyReader.Cancel()
	reader.mu.Lock()
	reader.cancelCalls++
	reader.cancelReturned = &result
	reader.mu.Unlock()
	reader.observer.send(reader.frame("cancel_result"))
	return result
}

func (reader *adminConsoleObservedReader) Close() error {
	reader.mu.Lock()
	reader.closeCalls++
	reader.closeStarted, reader.closeCompleted, reader.closeActive = reader.started, reader.completed, reader.active
	reader.closeComplete = reader.active == 0 && reader.started == reader.completed
	validOrder := reader.closeCalls == 1 && reader.closeComplete
	reader.mu.Unlock()
	if !validOrder {
		// Do not abort cleanup: observe the ordering failure, then call real Close.
		reader.test.Error("console reader close did not follow completed reads exactly once")
	}
	var queued *adminConsoleQueueEvidence
	if reader.queueAtClose {
		if validOrder {
			queued = adminConsoleInjectQueuedRecords()
		} else {
			queued = &adminConsoleQueueEvidence{}
		}
	}
	err := reader.adminSetupKeyReader.Close()
	succeeded := err == nil
	if queued != nil {
		queued.complete(succeeded)
	}
	reader.mu.Lock()
	reader.closeSucceeded = &succeeded
	reader.queueEvidence = queued
	reader.mu.Unlock()
	if queued != nil {
		reader.observer.send(reader.frame("queue_cleanup"))
	}
	reader.observer.send(reader.frame("close_result"))
	return err
}

// This child runs only inside the owned ConPTY controller. It observes natural
// dependency behavior without replacing reads, cancellation results, or cleanup.
func TestAdminSetupConsoleObserverChild(t *testing.T) {
	address := os.Getenv("OPENAI_ADMIN_CONSOLE_CONTROL")
	if address == "" {
		t.Skip("requires the owned Windows console controller")
	}
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		t.Fatal("console observer control must use a loopback address")
	}
	nonce := os.Getenv("OPENAI_ADMIN_CONSOLE_NONCE")
	if len(nonce) < 32 {
		t.Fatal("console observer requires a controller nonce")
	}
	scenario := os.Getenv("OPENAI_ADMIN_CONSOLE_SCENARIO")
	phases := []string{"success1", "cancel_partial", "success2"}
	switch scenario {
	case "", "original":
		scenario = "original"
	case "success_only":
		phases = []string{"success1", "success2"}
	case "cancel_first":
		phases = []string{"cancel_partial", "success2"}
	case "cancel_plain", "split_final":
		// The controller changes only its input framing for these comparisons.
	case "queue_startup":
		phases = []string{"success1"}
	case "queue_success":
		phases = []string{"success1", "success2"}
	case "queue_cancel":
		phases = []string{"cancel_partial", "success2"}
	default:
		t.Fatal("console observer received an unsupported diagnostic scenario")
	}
	readerMode := os.Getenv("OPENAI_ADMIN_CONSOLE_READER")
	if readerMode != "" && readerMode != "production" {
		t.Fatal("console observer requires the production reader")
	}
	readerMode = "production"
	expectedReaderType := adminSetupConsoleReaderType
	original, err := adminConsoleGetModes()
	if err != nil {
		adminConsoleLogHandleFailure(t, "original")
		t.Fatal("console observer requires console stdin, stdout, and stderr")
	}
	connection, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		t.Fatal("console observer could not connect to its controller")
	}
	defer connection.Close()
	if connection.SetDeadline(time.Now().Add(45*time.Second)) != nil {
		t.Fatal("console observer could not bound its control channel")
	}
	observer := &adminConsoleObserver{encoder: json.NewEncoder(connection), controls: make(chan adminConsoleControl, 1)}
	observer.send(map[string]any{"event": "hello", "nonce": nonce, "original": original, "scenario": scenario, "reader_mode": readerMode})
	// This bounds test-control metadata only, never key input or API payloads.
	decoder := json.NewDecoder(io.LimitReader(connection, 64<<10))
	var start adminConsoleControl
	if decoder.Decode(&start) != nil || start.Action != "start" || start.Nonce != nonce {
		t.Fatal("console observer controller authentication failed")
	}
	controlDone := make(chan struct{})
	controlStop := make(chan struct{})
	defer func() { close(controlStop); connection.Close(); <-controlDone }()
	go func() {
		defer close(controlDone)
		defer close(observer.controls)
		for {
			var control adminConsoleControl
			if decoder.Decode(&control) != nil {
				return
			}
			select {
			case observer.controls <- control:
			case <-controlStop:
				return
			}
		}
	}()
	checks := adminSetupConsoleReaderContractChecks()
	passed := len(checks) == 37
	for _, ok := range checks {
		passed = passed && ok
	}
	observer.send(map[string]any{"event": "reader_checks", "passed": passed, "checks": checks, "reader_mode": readerMode})
	if !passed {
		t.Fatal("production reader record conversion or ABI checks failed")
	}

	for _, phase := range phases {
		expectedKey := "sk-admin-SYNTHETIC-console-only"
		if phase == "success2" {
			expectedKey = "sk-admin-SYNTHETIC-console-next"
		}
		before, modeErr := adminConsoleGetModes()
		if modeErr != nil {
			adminConsoleLogHandleFailure(t, phase+" before")
		}
		if modeErr != nil || before != original {
			t.Fatal("console modes changed before the next observer phase")
		}
		ctx, cancel := context.WithCancel(context.Background())
		var cancelControl <-chan bool
		if phase == "cancel_partial" {
			received := make(chan bool, 1)
			cancelControl = received
			go func() {
				select {
				case control, open := <-observer.controls:
					valid := open && control.Action == "cancel" && control.Phase == "cancel_partial"
					received <- valid
					cancel()
				case <-ctx.Done():
					received <- false
				}
			}()
		}
		var reader *adminConsoleObservedReader
		var activeModes adminConsoleModes
		startupQueuePassed := true
		key, readErr := readAdminSetupKeyWithReader(ctx, os.Stdin, os.Stderr, func(input io.Reader) (adminSetupKeyReader, error) {
			var startupQueue *adminConsoleQueueEvidence
			if scenario == "queue_startup" {
				startupQueue = adminConsoleInjectQueuedRecords()
			}
			actual, createErr := newAdminSetupKeyReader(input)
			if startupQueue != nil {
				startupQueue.complete(createErr == nil)
				startupQueuePassed = startupQueue.Passed
				observer.send(map[string]any{"event": "startup_queue", "phase": phase, "queue": startupQueue, "passed": startupQueuePassed})
			}
			if createErr != nil {
				return nil, createErr
			}
			activeModes, modeErr = adminConsoleGetModes()
			if modeErr != nil {
				adminConsoleLogHandleFailure(t, phase+" active")
				actual.Close()
				return nil, modeErr
			}
			reader = &adminConsoleObservedReader{
				adminSetupKeyReader: actual, observer: observer, test: t, phase: phase, readerType: fmt.Sprintf("%T", actual),
				lastReadError: "not-read", firstByteCategory: "none", lastByteCategory: "none",
				byteCategories: map[string]int{"CR": 0, "LF": 0, "ctrl-C": 0, "ctrl-D": 0, "ascii-printable": 0, "ESC": 0, "other": 0},
				queueAtClose:   (scenario == "queue_success" && phase == "success1") || (scenario == "queue_cancel" && phase == "cancel_partial"),
			}
			frame := reader.frame("ready")
			frame["original"], frame["active"] = before, activeModes
			observer.send(frame)
			return reader, nil
		})
		// Snapshot before test cleanup can cancel anything or allow a later read
		// completion to hide a helper that returned with an active reader.
		var frame map[string]any
		if reader != nil {
			frame = reader.frame("phase_complete")
		}
		restored, restoreErr := adminConsoleGetModes()
		if restoreErr != nil {
			adminConsoleLogHandleFailure(t, phase+" restored")
		}
		cancel()
		keyMatches := string(key) == expectedKey
		keyEmpty := len(key) == 0
		clear(key)
		if reader == nil {
			t.Fatal("console observer did not construct the native reader")
		}
		readsComplete := frame["started"] == frame["completed"] && frame["active_reads"] == 0
		contextCanceled := errors.Is(readErr, context.Canceled)
		controlValid := true
		if cancelControl != nil {
			controlValid = <-cancelControl
		}
		observerHealthy := observer.healthy()
		queueCleanupPassed := reader.queueEvidence == nil || reader.queueEvidence.Passed
		valid := observerHealthy && restoreErr == nil && restored == before && readsComplete &&
			startupQueuePassed && queueCleanupPassed &&
			frame["started"].(int) > 0 && frame["cancel_calls"] == 1 && controlValid &&
			frame["close_calls"] == 1 && frame["reads_complete_at_close"] == true && frame["close_succeeded"] == true &&
			reader.readerType == expectedReaderType &&
			activeModes.Stdin&(windows.ENABLE_ECHO_INPUT|windows.ENABLE_LINE_INPUT) == 0 &&
			activeModes.Stdin&windows.ENABLE_VIRTUAL_TERMINAL_INPUT != 0
		if phase == "cancel_partial" {
			valid = valid && contextCanceled && keyEmpty
		} else {
			valid = valid && readErr == nil && keyMatches
		}
		frame["original"], frame["active"], frame["restored"] = before, activeModes, restored
		frame["key_matches"], frame["context_canceled"], frame["reads_complete"], frame["passed"] = keyMatches, contextCanceled, readsComplete, valid
		frame["read_error_category"], frame["key_empty"] = adminConsoleKeyErrorCategory(readErr), keyEmpty
		frame["restored_modes_match"], frame["observer_healthy"], frame["control_valid"] = restored == before, observerHealthy, controlValid
		frame["predicates"] = map[string]bool{
			"startup_queue": startupQueuePassed, "queue_cleanup": queueCleanupPassed,
			"observer_healthy": observerHealthy, "restore_succeeded": restoreErr == nil, "restored_modes_match": restored == before,
			"reads_complete": readsComplete, "read_started": frame["started"].(int) > 0,
			"cancel_called_once": frame["cancel_calls"] == 1, "control_valid": controlValid,
			"close_called_once": frame["close_calls"] == 1, "reads_complete_at_close": frame["reads_complete_at_close"] == true,
			"close_succeeded": frame["close_succeeded"] == true, "reader_is_native": reader.readerType == expectedReaderType,
			"echo_disabled":       activeModes.Stdin&windows.ENABLE_ECHO_INPUT == 0,
			"line_input_disabled": activeModes.Stdin&windows.ENABLE_LINE_INPUT == 0,
			"vt_input_enabled":    activeModes.Stdin&windows.ENABLE_VIRTUAL_TERMINAL_INPUT != 0,
			"phase_result":        (phase == "cancel_partial" && contextCanceled && keyEmpty) || (phase != "cancel_partial" && readErr == nil && keyMatches),
		}
		observer.send(frame)
		if !valid {
			t.Fatal("console observer phase failed its lifecycle contract")
		}
	}
	observer.send(map[string]any{"event": "done", "passed": true, "phases": len(phases)})
	ack, open := <-observer.controls
	if !open || ack.Action != "ack" || !observer.healthy() {
		t.Fatal("console observer did not receive its final acknowledgement")
	}
}
