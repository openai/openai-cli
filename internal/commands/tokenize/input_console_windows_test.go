//go:build windows

package tokenize

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	consolefixture "github.com/openai/openai-cli/internal/testutil/console"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

const tokenizerConsolePaste = "漢字 👩‍💻 e\u0301 \ufffd"

type tokenizerConsoleState struct {
	modes             consolefixture.Modes
	inputCP, outputCP uint32
}

func tokenizerConsoleSnapshot() (tokenizerConsoleState, error) {
	var state tokenizerConsoleState
	var modeErr, inputErr, outputErr error
	state.modes, modeErr = consolefixture.GetModes()
	state.inputCP, inputErr = windows.GetConsoleCP()
	state.outputCP, outputErr = windows.GetConsoleOutputCP()
	return state, errors.Join(modeErr, inputErr, outputErr)
}

func tokenizerConsoleRestore(state tokenizerConsoleState) error {
	var failures []error
	for _, item := range []struct {
		file *os.File
		mode uint32
	}{{os.Stdin, state.modes.Stdin}, {os.Stdout, state.modes.Stdout}, {os.Stderr, state.modes.Stderr}} {
		failures = append(failures, windows.SetConsoleMode(windows.Handle(item.file.Fd()), item.mode))
	}
	return errors.Join(append(failures, windows.SetConsoleCP(state.inputCP), windows.SetConsoleOutputCP(state.outputCP))...)
}

func tokenizerConsoleRecords(text string) []cliterminal.ConsoleInputRecord {
	units := utf16.Encode([]rune(text))
	records := make([]cliterminal.ConsoleInputRecord, len(units))
	for i, unit := range units {
		records[i] = cliterminal.ConsoleInputRecord{EventType: 1, KeyDown: 1, Repeat: 1, Character: unit}
	}
	return records
}

func tokenizerConsoleWrite(records []cliterminal.ConsoleInputRecord) error {
	// Accepted record counts do not prove text preservation; the model checks exact UTF-8 separately.
	written, err := consolefixture.WriteRecords(records)
	if err != nil {
		return err
	}
	if written != uint32(len(records)) {
		return errors.New("native tokenizer fixture write was incomplete")
	}
	return nil
}

type tokenizerConsoleDelivery struct {
	err             error
	boundaryReached bool
}

func tokenizerConsoleDeliver(ctx context.Context, cancel context.CancelFunc, scenario string, bridge *tokenizerInputBridge, program *tea.Program, emptyPaste <-chan struct{}) tokenizerConsoleDelivery {
	if strings.HasPrefix(scenario, "unicode_") {
		records := tokenizerConsoleRecords(tokenizerPasteStart + tokenizerConsolePaste + tokenizerPasteEnd + "\ufffd\x03")
		defer clear(records)
		err := tokenizerConsoleWrite(records)
		return tokenizerConsoleDelivery{err: err, boundaryReached: err == nil}
	}

	records := tokenizerConsoleRecords(tokenizerPasteStart + tokenizerPasteEnd)
	defer clear(records)
	if err := tokenizerConsoleWrite(records); err != nil {
		return tokenizerConsoleDelivery{err: err}
	}
	select {
	case <-ctx.Done():
		return tokenizerConsoleDelivery{err: ctx.Err()}
	case <-emptyPaste:
	}
	select {
	case <-bridge.Done():
		return tokenizerConsoleDelivery{err: errors.New("input ended before the native cancellation boundary")}
	default:
	}
	if strings.HasPrefix(scenario, "cancel_") {
		cancel()
		return tokenizerConsoleDelivery{boundaryReached: true}
	}
	// The runner closes the bridge only after Bubble Tea returns.
	program.Quit()
	return tokenizerConsoleDelivery{boundaryReached: true}
}

func tokenizerConsoleRun(t *testing.T, observer *consolefixture.Observer, scenario string, inputCP uint32, original tokenizerConsoleState) map[string]any {
	t.Helper()
	restored := false
	defer func() {
		if !restored {
			require.NoError(t, tokenizerConsoleRestore(original))
		}
	}()
	require.NoError(t, windows.SetConsoleCP(inputCP))
	_, err := uv.NewConsole(os.Stdin, os.Stdout, os.Environ()).MakeRaw()
	require.NoError(t, err)
	active, err := tokenizerConsoleSnapshot()
	require.NoError(t, err)
	require.Equal(t, inputCP, active.inputCP)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	bridge, err := newTokenizerInputBridge(ctx, os.Stdin)
	require.NoError(t, err)
	defer bridge.Close()
	sourceActive, sourceStateErr := tokenizerConsoleSnapshot()
	emptyPaste := make(chan struct{}, 1)
	model := &tokenizerInputProgramModel{tokenizerEditor: testTokenizerEditor(), bridge: bridge,
		onMessage: func(message tea.Msg) {
			if paste, ok := message.(tea.PasteMsg); ok && paste.Content == "" {
				select {
				case emptyPaste <- struct{}{}:
				default:
				}
			}
		}}
	program := tea.NewProgram(model, tea.WithInput(bridge.Input()), tea.WithOutput(io.Discard),
		tea.WithContext(ctx), tea.WithWindowSize(80, 24), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	observer.Send(map[string]any{"event": "ready", "scenario": "tokenizer/" + scenario, "active": sourceActive.modes,
		"active_input_cp": sourceActive.inputCP, "active_output_cp": sourceActive.outputCP})
	delivered := make(chan tokenizerConsoleDelivery, 1)
	go func() {
		result := tokenizerConsoleDeliver(ctx, cancel, scenario, bridge, program, emptyPaste)
		if result.err != nil {
			cancel()
		}
		delivered <- result
	}()
	_, runErr := program.Run()
	delivery := <-delivered
	contextErr := ctx.Err()
	want, wantExit := "", 0
	operationCompleted := runErr == nil
	if strings.HasPrefix(scenario, "unicode_") {
		want, wantExit = tokenizerConsolePaste+"\ufffd", 130
		operationCompleted = runErr == nil && model.quit
	} else if strings.HasPrefix(scenario, "cancel_") {
		operationCompleted = errors.Is(runErr, context.Canceled) && errors.Is(contextErr, context.Canceled)
	}
	digest := sha256.Sum256([]byte(model.text))
	// Preserve the model observation even if a baseline reader cannot finish cleanup.
	observer.Send(map[string]any{
		"event": "model_complete", "scenario": "tokenizer/" + scenario,
		"input_sha256": fmt.Sprintf("%x", digest), "input_bytes": len(model.text), "model_exit": model.exitCode,
		"fixture_written": delivery.err == nil, "operation_completed": operationCompleted,
		"deadline_not_exceeded": !errors.Is(contextErr, context.DeadlineExceeded),
	})
	started := time.Now()
	closeErr := bridge.Close()
	closeElapsed := time.Since(started)
	repeatedCloseErr := bridge.Close()
	readerJoined, bridgeClosed := false, false
	select {
	case <-bridge.readDone:
		readerJoined = true
	default:
	}
	select {
	case <-bridge.Done():
		bridgeClosed = true
	default:
	}
	afterClose, stateErr := tokenizerConsoleSnapshot()
	checks := map[string]bool{
		"fixture_written": delivery.err == nil, "lifecycle_boundary_reached": delivery.boundaryReached,
		"input_matches":      model.text == want,
		"model_exit_matches": model.exitCode == wantExit, "operation_completed": operationCompleted,
		"deadline_not_exceeded": !errors.Is(contextErr, context.DeadlineExceeded),
		"bridge_closed":         bridgeClosed, "reader_joined": readerJoined,
		"close_succeeded": closeErr == nil, "repeated_close_succeeded": repeatedCloseErr == nil,
		"close_bounded":              closeElapsed < time.Second,
		"borrowed_input_open":        stateErr == nil,
		"active_modes_unchanged":     sourceStateErr == nil && stateErr == nil && sourceActive.modes == active.modes && afterClose.modes == active.modes,
		"active_codepages_unchanged": sourceStateErr == nil && stateErr == nil && sourceActive.inputCP == active.inputCP && sourceActive.outputCP == active.outputCP && afterClose.inputCP == active.inputCP && afterClose.outputCP == active.outputCP,
	}
	restoreErr := tokenizerConsoleRestore(original)
	final, finalErr := tokenizerConsoleSnapshot()
	restored = restoreErr == nil && finalErr == nil && final == original
	checks["restored_modes_match"] = restoreErr == nil && finalErr == nil && final.modes == original.modes
	checks["restored_codepages_match"] = restoreErr == nil && finalErr == nil && final.inputCP == original.inputCP && final.outputCP == original.outputCP
	checks["observer_healthy"] = observer.Healthy()
	return map[string]any{
		"event": "tokenizer_complete", "scenario": "tokenizer/" + scenario, "checks": checks,
		"original": original.modes, "restored": final.modes,
		"original_input_cp": original.inputCP, "original_output_cp": original.outputCP,
		"restored_input_cp": final.inputCP, "restored_output_cp": final.outputCP,
		"active_input_cp": active.inputCP, "active_output_cp": active.outputCP,
		// This is the model's status contract, not the child test process's exit code.
		"input_sha256": fmt.Sprintf("%x", digest), "input_bytes": len(model.text), "model_exit": model.exitCode,
	}
}

// The existing owned ConPTY controller launches this child. An ordinary test run skips it explicitly.
func TestTokenizerInputConsoleChild(t *testing.T) {
	address := os.Getenv("OPENAI_ADMIN_CONSOLE_CONTROL")
	if address == "" {
		t.Skip("requires the owned Windows console controller")
	}
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		t.Fatal("tokenizer console control must use a loopback address")
	}
	nonce := os.Getenv("OPENAI_ADMIN_CONSOLE_NONCE")
	if len(nonce) < 32 {
		t.Fatal("tokenizer console control requires a nonce")
	}
	scenario := os.Getenv("OPENAI_TOKENIZER_CONSOLE_SCENARIO")
	inputCP := uint32(437)
	switch scenario {
	case "unicode_cp437", "cancel_idle", "close_idle":
	case "unicode_cp65001":
		inputCP = 65001
	default:
		t.Fatal("unsupported tokenizer console fixture")
	}
	original, err := tokenizerConsoleSnapshot()
	require.NoError(t, err, "tokenizer console child requires native console standard handles")
	connection, err := net.DialTimeout("tcp", address, 5*time.Second)
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.SetDeadline(time.Now().Add(15*time.Second)))
	observer := consolefixture.NewObserver(json.NewEncoder(connection))
	observer.Send(map[string]any{"event": "hello", "nonce": nonce, "original": original.modes,
		"scenario": "tokenizer/" + scenario, "original_input_cp": original.inputCP, "original_output_cp": original.outputCP})
	decoder := json.NewDecoder(io.LimitReader(connection, 64<<10))
	var start consolefixture.Control
	if decoder.Decode(&start) != nil || start.Action != "start" || start.Nonce != nonce {
		t.Fatal("tokenizer console controller authentication failed")
	}
	frame := tokenizerConsoleRun(t, observer, scenario, inputCP, original)
	checks := frame["checks"].(map[string]bool)
	passed := true
	for _, valid := range checks {
		passed = passed && valid
	}
	frame["passed"] = passed
	observer.Send(frame)
	var ack consolefixture.Control
	if decoder.Decode(&ack) != nil || ack.Action != "ack" || !observer.Healthy() {
		t.Fatal("tokenizer console child did not receive its final acknowledgement")
	}
	for name, valid := range checks {
		if !valid {
			t.Errorf("native tokenizer contract failed: %s", name)
		}
	}
}
