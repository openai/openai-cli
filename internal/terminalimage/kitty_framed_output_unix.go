//go:build darwin || linux

package terminalimage

import (
	"bufio"
	"errors"
	"io"
)

// A small complete frame fits Darwin's minimum writable tty queue headroom.
// Readiness is not a reservation: the writer must still reject partial writes.
const kittyTTYFrameSize = 64

type kittyFramePipeWriter struct{ io.Writer }

func (kittyFramePipeWriter) kittyFrameSize() int { return kittyTTYFrameSize }

func (w kittyFramePipeWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if len(data) > kittyTTYFrameSize {
		return 0, errors.New("Kitty frame exceeds the transport budget")
	}
	var record [1 + kittyTTYFrameSize]byte
	record[0] = byte(len(data))
	copy(record[1:], data)
	n, err := w.Writer.Write(record[:1+len(data)])
	if err == nil && n != len(data)+1 {
		err = io.ErrShortWrite
	}
	return max(0, n-1), err
}

// The pipe is a byte stream. Length records preserve each complete APC or reset.
func readKittyFrames(input io.Reader, write func([]byte) error) error {
	reader := bufio.NewReaderSize(input, 32*1024)
	var frame [kittyTTYFrameSize]byte
	for {
		size, err := reader.ReadByte()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if size == 0 || int(size) > len(frame) {
			return errors.New("invalid Kitty frame size")
		}
		if _, err := io.ReadFull(reader, frame[:size]); err != nil {
			return err
		}
		if err := write(frame[:size]); err != nil {
			return err
		}
	}
}
