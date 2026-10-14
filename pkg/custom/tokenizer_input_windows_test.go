//go:build windows

package custom

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/muesli/cancelreader"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func tokenizerConsoleKey(value uint16) consoleInputRecord {
	return consoleInputRecord{eventType: 1, keyDown: 1, repeat: 1, character: value}
}

func tokenizerConsoleText(value string) []consoleInputRecord {
	var records []consoleInputRecord
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
			for _, batch := range [][]consoleInputRecord{records[:split], records[split:]} {
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
	repeated.repeat = 3
	high, low := tokenizerConsoleKey(0xd83d), tokenizerConsoleKey(0xde00)
	high.repeat, low.repeat = 3, 3
	zeroRepeat := tokenizerConsoleKey('z')
	zeroRepeat.repeat = 0
	release := tokenizerConsoleKey('x')
	release.keyDown = 0
	modifier := tokenizerConsoleKey(0)
	modifier.virtualKey = 0x10
	arrow := modifier
	arrow.virtualKey = 0x25
	ctrlNUL := modifier
	ctrlNUL.virtualKey, ctrlNUL.controlState = 0x20, 0x0008
	composition := tokenizerConsoleKey('1')
	composition.virtualKey, composition.controlState = 0x61, 0x0002
	altRelease := tokenizerConsoleKey('é')
	altRelease.keyDown, altRelease.virtualKey = 0, 0x12
	altNUL := altRelease
	altNUL.character = 0
	altHigh, altLow := altRelease, altRelease
	altHigh.character, altLow.character = 0xd83d, 0xde00
	releaseHigh, releaseLow := high, low
	releaseHigh.keyDown, releaseLow.keyDown = 0, 0
	for _, test := range []struct {
		name    string
		records []consoleInputRecord
		want    string
	}{
		{"repeats", []consoleInputRecord{repeated, high, low, zeroRepeat}, "ééé😀😀😀z"},
		{"nontext", []consoleInputRecord{{eventType: 4}, modifier, arrow, release, tokenizerConsoleKey('a')}, "a"},
		{"translated_NUL", []consoleInputRecord{tokenizerConsoleKey(0), ctrlNUL}, "\x00\x00"},
		{"Alt_composition", []consoleInputRecord{composition, altRelease, composition, altNUL}, "é\x00"},
		{"Alt_surrogate_pair", []consoleInputRecord{altHigh, altLow}, "😀"},
		{"ordinary_surrogate_releases", []consoleInputRecord{high, releaseHigh, releaseLow, low}, "😀😀😀"},
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
	repeatedLow.repeat = 2
	altLow := low
	altLow.keyDown, altLow.virtualKey = 0, 0x12
	for _, test := range []struct {
		name    string
		records []consoleInputRecord
	}{
		{"isolated_low", []consoleInputRecord{low}},
		{"following_text", []consoleInputRecord{high, tokenizerConsoleKey('x')}},
		{"repeated_high", []consoleInputRecord{high, high}},
		{"repeat_mismatch", []consoleInputRecord{high, repeatedLow}},
		{"channel_mismatch", []consoleInputRecord{high, altLow}},
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
			require.Equal(t, [32]consoleInputRecord{}, reader.records)
		})
	}
}

func TestTokenizerConsoleInputBoundedRepeatsAndCancellation(t *testing.T) {
	reader := &tokenizerConsoleInput{}
	reader.records[0] = tokenizerConsoleKey('x')
	reader.records[0].repeat = 0xffff
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
