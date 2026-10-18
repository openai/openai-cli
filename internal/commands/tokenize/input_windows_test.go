//go:build windows

package tokenize

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/muesli/cancelreader"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func tokenizerConsoleKey(value uint16) cliterminal.ConsoleInputRecord {
	return cliterminal.ConsoleInputRecord{EventType: 1, KeyDown: 1, Repeat: 1, Character: value}
}

func tokenizerConsoleText(value string) []cliterminal.ConsoleInputRecord {
	var records []cliterminal.ConsoleInputRecord
	for _, unit := range utf16.Encode([]rune(value)) {
		records = append(records, tokenizerConsoleKey(unit))
	}
	return records
}

func TestTokenizerConsoleInputPreservesUnicode(t *testing.T) {
	text := "A\u0301中文😀\ufffd\x00\x1a\r\n\t\x1b[200~x\x1b[201~"
	records := tokenizerConsoleText(text)
	for _, width := range []int{1, 2, 3, 4, 31} {
		for split := 0; split <= len(records); split++ {
			reader := &tokenizerConsoleInput{}
			var output bytes.Buffer
			for _, batch := range [][]cliterminal.ConsoleInputRecord{records[:split], records[split:]} {
				reader.count, reader.next = copy(reader.records[:], batch), 0
				require.Equal(t, len(batch), reader.count)
				for reader.next < reader.count || reader.repeats != 0 {
					buffer := make([]byte, width)
					n, err := reader.copyRecords(buffer)
					require.NoError(t, err, "buffer=%d split=%d", width, split)
					output.Write(buffer[:n])
				}
			}
			require.Zero(t, reader.high)
			require.Equal(t, text, output.String(), "buffer=%d split=%d", width, split)
		}
	}
}

func TestTokenizerConsoleInputRecordSemantics(t *testing.T) {
	repeated := tokenizerConsoleKey('é')
	repeated.Repeat = 3
	high, low := tokenizerConsoleKey(0xd83d), tokenizerConsoleKey(0xde00)
	high.Repeat, low.Repeat = 3, 3
	zeroRepeat := tokenizerConsoleKey('z')
	zeroRepeat.Repeat = 0
	release := tokenizerConsoleKey('x')
	release.KeyDown = 0
	modifier := tokenizerConsoleKey(0)
	modifier.VirtualKey = 0x10
	arrow := modifier
	arrow.VirtualKey = 0x25
	ctrlNUL := modifier
	ctrlNUL.VirtualKey, ctrlNUL.ControlState = 0x20, 0x0008
	composition := tokenizerConsoleKey('1')
	composition.VirtualKey, composition.ControlState = 0x61, 0x0002
	altRelease := tokenizerConsoleKey('é')
	altRelease.KeyDown, altRelease.VirtualKey = 0, 0x12
	altNUL := altRelease
	altNUL.Character = 0
	altHigh, altLow := altRelease, altRelease
	altHigh.Character, altLow.Character = 0xd83d, 0xde00
	releaseHigh, releaseLow := high, low
	releaseHigh.KeyDown, releaseLow.KeyDown = 0, 0
	for _, test := range []struct {
		name    string
		records []cliterminal.ConsoleInputRecord
		want    string
	}{
		{"repeats", []cliterminal.ConsoleInputRecord{repeated, high, low, zeroRepeat}, "ééé😀😀😀z"},
		{"nontext", []cliterminal.ConsoleInputRecord{{EventType: 4}, modifier, arrow, release, tokenizerConsoleKey('a')}, "a"},
		{"translated_NUL", []cliterminal.ConsoleInputRecord{tokenizerConsoleKey(0), ctrlNUL}, "\x00\x00"},
		{"Alt_composition", []cliterminal.ConsoleInputRecord{composition, altRelease, composition, altNUL}, "é\x00"},
		{"Alt_surrogate_pair", []cliterminal.ConsoleInputRecord{altHigh, altLow}, "😀"},
		{"ordinary_surrogate_releases", []cliterminal.ConsoleInputRecord{high, releaseHigh, releaseLow, low}, "😀😀😀"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &tokenizerConsoleInput{}
			reader.count = copy(reader.records[:], test.records)
			var output bytes.Buffer
			for reader.next < reader.count || reader.repeats != 0 {
				var buffer [1]byte
				n, err := reader.copyRecords(buffer[:])
				require.NoError(t, err)
				output.Write(buffer[:n])
			}
			require.Zero(t, reader.high)
			require.Equal(t, test.want, output.String())
		})
	}
}

func TestTokenizerConsoleInputRejectsMalformedUTF16(t *testing.T) {
	high, low := tokenizerConsoleKey(0xd83d), tokenizerConsoleKey(0xde00)
	repeatedLow := low
	repeatedLow.Repeat = 2
	altLow := low
	altLow.KeyDown, altLow.VirtualKey = 0, 0x12
	for _, test := range []struct {
		name    string
		records []cliterminal.ConsoleInputRecord
	}{
		{"isolated_low", []cliterminal.ConsoleInputRecord{low}},
		{"following_text", []cliterminal.ConsoleInputRecord{high, tokenizerConsoleKey('x')}},
		{"repeated_high", []cliterminal.ConsoleInputRecord{high, high}},
		{"repeat_mismatch", []cliterminal.ConsoleInputRecord{high, repeatedLow}},
		{"channel_mismatch", []cliterminal.ConsoleInputRecord{high, altLow}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &tokenizerConsoleInput{}
			reader.records[0] = tokenizerConsoleKey('a')
			reader.count = 1 + copy(reader.records[1:], test.records)
			var output [16]byte
			n, err := reader.Read(output[:])
			require.ErrorIs(t, err, errTokenizerConsoleUTF16)
			require.Equal(t, "a", string(output[:n]), "malformed input must not become literal replacement text")
			require.EqualError(t, err, "invalid UTF-16 console input")
			n, err = reader.Read(output[:])
			require.Zero(t, n)
			require.ErrorIs(t, err, errTokenizerConsoleUTF16, "source failures remain observable")
			require.Zero(t, reader.high)
			require.Equal(t, [32]cliterminal.ConsoleInputRecord{}, reader.records)
		})
	}
}

func TestTokenizerConsoleInputBoundedRepeatsAndCancellation(t *testing.T) {
	reader := &tokenizerConsoleInput{}
	reader.records[0] = tokenizerConsoleKey('x')
	reader.records[0].Repeat = 0xffff
	reader.count = 1
	var output [1]byte
	n, err := reader.Read(output[:])
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, byte('x'), output[0])
	require.Equal(t, uint16(0xfffe), reader.repeats)
	require.True(t, reader.Cancel())
	n, err = reader.Read(output[:])
	require.Zero(t, n)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, reader.repeats)
	require.Equal(t, [4]byte{}, reader.pending)
	require.NoError(t, reader.Close())
	require.NoError(t, reader.Close())

	reader = &tokenizerConsoleInput{}
	reader.records[0], reader.count = tokenizerConsoleKey(0xd83d), 1
	n, err = reader.copyRecords(output[:])
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, uint16(0xd83d), reader.high)
	require.True(t, reader.Cancel())
	n, err = reader.Read(output[:])
	require.Zero(t, n)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, reader.high, "cancellation clears an unfinished pair without inserting text")
}

func TestTokenizerConsoleInputClosePreservesBorrowedHandle(t *testing.T) {
	input, output, err := os.Pipe()
	require.NoError(t, err)
	defer input.Close()
	defer output.Close()
	reader := &tokenizerConsoleInput{input: windows.Handle(input.Fd())}
	require.NoError(t, reader.Close())
	require.NoError(t, reader.Close())
	_, err = io.WriteString(output, "x")
	require.NoError(t, err)
	var data [1]byte
	_, err = io.ReadFull(input, data[:])
	require.NoError(t, err)
	require.Equal(t, "x", string(data[:]))
}

func TestTokenizerConsoleInputReadFailure(t *testing.T) {
	reader := &tokenizerConsoleInput{input: windows.InvalidHandle}
	var output [1]byte
	n, err := reader.Read(output[:])
	require.Zero(t, n)
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "read console input records:"))
	_, repeated := reader.Read(output[:])
	require.Same(t, err, repeated)
	require.True(t, reader.Cancel())
	_, canceled := reader.Read(output[:])
	require.ErrorIs(t, canceled, context.Canceled, "cancellation takes precedence over a stored read error")
	require.NoError(t, reader.Close())
}

func TestTokenizerInputSourcePreservesNonConsoleBytes(t *testing.T) {
	input, output, err := os.Pipe()
	require.NoError(t, err)
	defer input.Close()
	defer output.Close()
	source, err := newTokenizerInputSource(input)
	require.NoError(t, err)
	defer source.Close()
	_, native := source.(*tokenizerConsoleInput)
	require.False(t, native, "a pipe must retain the existing byte-reader fallback")
	payload := append([]byte("漢字😀\ufffd\r\n\x00\x1a\x1b[200~"), 0xff, 0xfe, 0xed, 0xa0, 0x80)
	n, err := output.Write(payload)
	require.NoError(t, err)
	require.Equal(t, len(payload), n)
	actual := make([]byte, len(payload))
	_, err = io.ReadFull(source, actual)
	require.NoError(t, err)
	require.Equal(t, payload, actual, "nonconsole input must not pass through UTF-16 decoding")

	_, err = io.WriteString(output, "kept")
	require.NoError(t, err)
	require.False(t, source.Cancel(), "the existing pipe fallback cannot interrupt an active read")
	n, err = source.Read(actual[:1])
	require.Zero(t, n)
	require.ErrorIs(t, err, cancelreader.ErrCanceled)
	require.NoError(t, source.Close())
	require.NoError(t, source.Close())
	var retained [4]byte
	_, err = io.ReadFull(input, retained[:])
	require.NoError(t, err)
	require.Equal(t, "kept", string(retained[:]), "cancel and close must retain the borrowed input and queued bytes")
}
