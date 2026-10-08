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
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
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
	mu             sync.Mutex
	observer       *adminConsoleObserver
	test           *testing.T
	phase          string
	readerType     string
	started        int
	completed      int
	active         int
	readBytes      int
	cancelCalls    int
	cancelReturned *bool
	closeCalls     int
	closeStarted   int
	closeCompleted int
	closeActive    int
	closeComplete  bool
	closeSucceeded *bool
}

func (reader *adminConsoleObservedReader) frame(event string) map[string]any {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	frame := map[string]any{
		"event": event, "phase": reader.phase, "reader_type": reader.readerType,
		"started": reader.started, "completed": reader.completed,
		"active_reads": reader.active, "read_bytes": reader.readBytes, "cancel_calls": reader.cancelCalls,
		"close_calls": reader.closeCalls,
	}
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
	reader.mu.Unlock()
	reader.observer.send(reader.frame("read_completed"))
	return n, err
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
	err := reader.adminSetupKeyReader.Close()
	succeeded := err == nil
	reader.mu.Lock()
	reader.closeSucceeded = &succeeded
	reader.mu.Unlock()
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
	original, err := adminConsoleGetModes()
	if err != nil {
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
	observer.send(map[string]any{"event": "hello", "nonce": nonce, "original": original})
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

	for _, phase := range []string{"success1", "cancel_partial", "success2"} {
		expectedKey := "sk-admin-SYNTHETIC-console-only"
		if phase == "success2" {
			expectedKey = "sk-admin-SYNTHETIC-console-next"
		}
		before, modeErr := adminConsoleGetModes()
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
		key, readErr := readAdminSetupKeyWithReader(ctx, os.Stdin, os.Stderr, func(input io.Reader) (adminSetupKeyReader, error) {
			actual, createErr := uv.NewCancelReader(input)
			if createErr != nil {
				return nil, createErr
			}
			activeModes, modeErr = adminConsoleGetModes()
			if modeErr != nil {
				actual.Close()
				return nil, modeErr
			}
			reader = &adminConsoleObservedReader{
				adminSetupKeyReader: actual, observer: observer, test: t, phase: phase, readerType: fmt.Sprintf("%T", actual),
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
		valid := observer.healthy() && restoreErr == nil && restored == before && readsComplete &&
			frame["started"].(int) > 0 && frame["cancel_calls"] == 1 && controlValid &&
			frame["close_calls"] == 1 && frame["reads_complete_at_close"] == true && frame["close_succeeded"] == true &&
			reader.readerType == "*uv.conInputReader" &&
			activeModes.Stdin&(windows.ENABLE_ECHO_INPUT|windows.ENABLE_LINE_INPUT) == 0 &&
			activeModes.Stdin&windows.ENABLE_VIRTUAL_TERMINAL_INPUT != 0
		if phase == "cancel_partial" {
			valid = valid && contextCanceled && keyEmpty
		} else {
			valid = valid && readErr == nil && keyMatches
		}
		frame["original"], frame["active"], frame["restored"] = before, activeModes, restored
		frame["key_matches"], frame["context_canceled"], frame["reads_complete"], frame["passed"] = keyMatches, contextCanceled, readsComplete, valid
		observer.send(frame)
		if !valid {
			t.Fatal("console observer phase failed its lifecycle contract")
		}
	}
	observer.send(map[string]any{"event": "done", "passed": true, "phases": 3})
	ack, open := <-observer.controls
	if !open || ack.Action != "ack" || !observer.healthy() {
		t.Fatal("console observer did not receive its final acknowledgement")
	}
}
