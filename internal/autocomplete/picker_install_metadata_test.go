package autocomplete

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/require"
)

func pickerStreamFixture(names ...string) []byte {
	var result []byte
	for index, name := range names {
		encoded := utf16.Encode([]rune(name))
		length := 24 + len(encoded)*2
		length = (length + 7) &^ 7
		entry := make([]byte, length)
		if index < len(names)-1 {
			binary.LittleEndian.PutUint32(entry, uint32(length))
		}
		binary.LittleEndian.PutUint32(entry[4:], uint32(len(encoded)*2))
		for offset, value := range encoded {
			binary.LittleEndian.PutUint16(entry[24+offset*2:], value)
		}
		result = append(result, entry...)
	}
	return result
}

func TestPickerFileStreamsPreserveOriginAndOtherAlternateStreams(t *testing.T) {
	require.NoError(t, validatePickerFileStreams(pickerStreamFixture("::$DATA")))
	for _, streams := range [][]string{
		{"::$DATA", ":Zone.Identifier:$DATA"},
		{":Zone.Identifier:$DATA", "::$DATA"},
		{"::$DATA", ":user-notes:$DATA"},
		{"::$DATA", ":ZONE.IDENTIFIER:$DATA"},
	} {
		require.ErrorContains(t, validatePickerFileStreams(pickerStreamFixture(streams...)), "protected alternate-stream metadata")
	}
}

func TestPickerFileStreamsRejectUnknownMetadata(t *testing.T) {
	for _, data := range [][]byte{nil, make([]byte, 24), {1}, pickerStreamFixture("::$DATA")[:25]} {
		require.Error(t, validatePickerFileStreams(data))
	}
	for _, offset := range []uint32{1, 24, 39, 4096, 0xffffffff} {
		data := pickerStreamFixture("::$DATA")
		binary.LittleEndian.PutUint32(data, offset)
		require.Error(t, validatePickerFileStreams(data))
	}
	for _, length := range []uint32{1, 0xfffffffe, 0xffffffff} {
		data := pickerStreamFixture("::$DATA")
		binary.LittleEndian.PutUint32(data[4:], length)
		require.Error(t, validatePickerFileStreams(data))
	}
}
