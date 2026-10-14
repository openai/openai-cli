//go:build windows

package custom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"unicode/utf16"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	xwindows "github.com/charmbracelet/x/windows"
	"golang.org/x/sys/windows"
)

var errTokenizerConsoleUTF16 = errors.New("invalid UTF-16 console input")

// Read wide console records before the raw bridge, without code-page conversion.
// The editor's console setup owns VT input mode and its restoration.
func newTokenizerInputSource(input *os.File) (tokenizerCancelableInput, error) {
	handle := windows.Handle(input.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return uv.NewCancelReader(input)
	}
	if err := consoleReadInputEx.Find(); err != nil {
		return nil, fmt.Errorf("prepare console input records: %w", err)
	}
	// Preserve NewCancelReader's startup clearing. Never drain on Cancel or Close.
	if err := xwindows.FlushConsoleInputBuffer(handle); err != nil {
		return nil, fmt.Errorf("clear pending console input: %w", err)
	}
	return &tokenizerConsoleInput{input: handle}, nil
}

type tokenizerConsoleInput struct {
	input    windows.Handle // Borrowed; the reader never closes or changes it.
	mu       sync.Mutex
	canceled atomic.Bool
	readErr  error

	records     [32]consoleInputRecord
	count, next int
	pending     [utf8.UTFMax]byte
	size, used  int
	repeats     uint16
	high        uint16
	highRepeat  uint16
	highChannel uint8
	altNumpad   bool
}

func (reader *tokenizerConsoleInput) Read(output []byte) (int, error) {
	if len(output) == 0 {
		return 0, nil
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	for {
		if reader.canceled.Load() {
			reader.clearInput()
			return 0, context.Canceled
		}
		if reader.readErr != nil {
			return 0, reader.readErr
		}
		n, err := reader.copyRecords(output)
		if reader.canceled.Load() {
			clear(output[:n])
			reader.clearInput()
			return 0, context.Canceled
		}
		if err != nil {
			reader.readErr = err
			reader.clearInput()
		}
		if n > 0 || err != nil {
			return n, err
		}
		clear(reader.records[:])
		reader.count, reader.next = 0, 0
		reader.count, err = readConsoleInputRecords(reader.input, reader.records[:])
		if err == nil && reader.count == 0 {
			// NOWAIT never leaves a pending read; the finite wait bounds cancellation.
			var state uint32
			state, err = windows.WaitForSingleObject(reader.input, 10)
			if err == nil && state != windows.WAIT_OBJECT_0 && state != uint32(windows.WAIT_TIMEOUT) {
				err = fmt.Errorf("wait for console input: unexpected state %d", state)
			}
		}
		if err != nil {
			reader.readErr = err
			reader.clearInput()
		}
	}
}

func (reader *tokenizerConsoleInput) Cancel() bool {
	reader.canceled.Store(true)
	return true
}

func (reader *tokenizerConsoleInput) Close() error {
	reader.Cancel()
	// Join any active Read before clearing its staging. Never cancel shared I/O.
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.clearInput()
	return nil
}

func (reader *tokenizerConsoleInput) clearInput() {
	clear(reader.records[:])
	clear(reader.pending[:])
	reader.count, reader.next, reader.size, reader.used = 0, 0, 0, 0
	reader.repeats, reader.high, reader.highRepeat, reader.highChannel = 0, 0, 0, 0
	reader.altNumpad = false
}

// Expand one scalar at a time. Even maximum native repeat counts use fixed storage.
func (reader *tokenizerConsoleInput) copyRecords(output []byte) (int, error) {
	written := 0
	for written < len(output) {
		if reader.canceled.Load() {
			return written, context.Canceled
		}
		if reader.repeats != 0 {
			n := copy(output[written:], reader.pending[reader.used:reader.size])
			written += n
			reader.used += n
			if reader.used == reader.size {
				reader.used = 0
				reader.repeats--
				if reader.repeats == 0 {
					clear(reader.pending[:])
					reader.size = 0
				}
			}
			continue
		}
		if reader.next == reader.count {
			return written, nil
		}
		record := reader.records[reader.next]
		reader.records[reader.next] = consoleInputRecord{}
		reader.next++
		channel := reader.characterChannel(record)
		if channel == 0 {
			continue
		}
		value := rune(record.character)
		repeat := max(record.repeat, 1)
		if reader.high != 0 {
			if value < 0xdc00 || value > 0xdfff || repeat != reader.highRepeat || channel != reader.highChannel {
				return written, errTokenizerConsoleUTF16
			}
			value = utf16.DecodeRune(rune(reader.high), value)
			reader.high, reader.highRepeat, reader.highChannel = 0, 0, 0
		} else if value >= 0xd800 && value <= 0xdbff {
			reader.high, reader.highRepeat, reader.highChannel = record.character, repeat, channel
			continue
		} else if value >= 0xdc00 && value <= 0xdfff {
			return written, errTokenizerConsoleUTF16
		}
		reader.size = utf8.EncodeRune(reader.pending[:], value)
		reader.repeats = repeat
	}
	return written, nil
}

// Channel identity prevents pairing a key press with unrelated Alt-release text.
// VT input already translates ordinary keys and paste markers into characters.
func (reader *tokenizerConsoleInput) characterChannel(record consoleInputRecord) uint8 {
	if record.eventType != 1 {
		return 0
	}
	isAlt := record.virtualKey == 0x12 || record.virtualKey == 0xa4 || record.virtualKey == 0xa5
	if record.keyDown == 0 {
		if !isAlt {
			return 0
		}
		composed := reader.altNumpad
		reader.altNumpad = false
		if record.character != 0 || composed {
			return 2
		}
		return 0
	}
	alt := record.controlState&0x0003 != 0
	ctrl := record.controlState&0x000c != 0
	if alt && !ctrl && record.virtualKey >= 0x60 && record.virtualKey <= 0x69 {
		reader.altNumpad = true
		return 0
	}
	if record.character == 0 && record.virtualKey != 0 && record.virtualKey != 0xe7 {
		// Ctrl+Space, Ctrl+2/@, and Ctrl+grave can translate to NUL.
		// Other characterless physical keys, including modifiers, carry no text.
		if !ctrl || record.virtualKey != 0x20 && record.virtualKey != 0x32 && record.virtualKey != 0xc0 {
			return 0
		}
	}
	return 1
}
