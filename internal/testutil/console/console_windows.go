//go:build windows

// Package console provides synthetic console records and synchronized observer
// frames for native Windows feature tests. It is imported only by test code.
package console

import (
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"sync"
	"unsafe"

	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"golang.org/x/sys/windows"
)

type Modes struct {
	Stdin  uint32 `json:"stdin"`
	Stdout uint32 `json:"stdout"`
	Stderr uint32 `json:"stderr"`
}

func GetModes() (Modes, error) {
	var modes Modes
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

type Control struct {
	Action string `json:"action"`
	Phase  string `json:"phase"`
	Nonce  string `json:"nonce"`
}

type Observer struct {
	mu      sync.Mutex
	encoder *json.Encoder
	failed  bool
}

func (observer *Observer) Send(frame map[string]any) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.encoder.Encode(frame) != nil {
		observer.failed = true
	}
}

func (observer *Observer) Healthy() bool {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return !observer.failed
}

func NewObserver(encoder *json.Encoder) *Observer { return &Observer{encoder: encoder} }

var writeInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("WriteConsoleInputW")

// Native fixture writer shared by parity and queue tests. The count describes
// records accepted by Windows, not bytes delivered to a particular reader.
func WriteRecords(records []cliterminal.ConsoleInputRecord) (uint32, error) {
	if len(records) == 0 {
		return 0, nil
	}
	if unsafe.Sizeof(records[0]) != 20 || uint64(len(records)) > uint64(^uint32(0)) {
		return 0, errors.New("native fixture record layout or count is invalid")
	}
	if err := writeInput.Find(); err != nil {
		return 0, err
	}
	input, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil {
		return 0, err
	}
	var written uint32
	result, _, callErr := writeInput.Call(uintptr(input), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&written)))
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
