package autocomplete

import (
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"unicode/utf16"
)

// Atomic replacement must not silently discard special bits on regular files.
// Directory modes are handled by the directory policy and are not replaced.
func checkPickerFileMode(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return errors.New("cannot inspect shell startup file mode")
	}
	if info.Mode().IsRegular() && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return errors.New("shell startup file has special permission bits; existing file was kept")
	}
	return nil
}

// Windows FILE_STREAM_INFO identifies streams on the opened file itself.
// Atomic replacement must not discard origin marks or other user metadata.
func validatePickerFileStreams(data []byte) error {
	for {
		if len(data) < 24 {
			return errors.New("cannot inspect shell startup file metadata")
		}
		next := uint64(binary.LittleEndian.Uint32(data[:4]))
		length := uint64(binary.LittleEndian.Uint32(data[4:8]))
		if length == 0 || length%2 != 0 || length > uint64(len(data)-24) {
			return errors.New("cannot inspect shell startup file metadata")
		}
		name := make([]uint16, length/2)
		for index := range name {
			name[index] = binary.LittleEndian.Uint16(data[24+index*2:])
		}
		if !strings.EqualFold(string(utf16.Decode(name)), "::$DATA") {
			return errors.New("shell startup file has protected alternate-stream metadata")
		}
		if next == 0 {
			return nil
		}
		if next < 24+length || next >= uint64(len(data)) || next%8 != 0 {
			return errors.New("cannot inspect shell startup file metadata")
		}
		data = data[next:]
	}
}
