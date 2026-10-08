package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/term"
)

var (
	errAdminSetupTerminal   = errors.New("Admin setup requires terminal input and error output.")
	errAdminSetupKeyEmpty   = errors.New("No admin API key entered.")
	errAdminSetupKeyInvalid = errors.New("Enter one admin API key without spaces, line breaks, or control characters.")
	errAdminSetupKeyIO      = errors.New("Could not read the admin API key or restore the terminal.")
)

type adminSetupKeyReader interface {
	io.ReadCloser
	Cancel() bool
}

// readAdminSetupKey returns a caller-owned buffer. The caller must clear it after use.
// It does not store the key in the environment, command flags, or persistent storage.
func readAdminSetupKey(ctx context.Context, input, output *os.File) (key []byte, err error) {
	return readAdminSetupKeyWithReader(ctx, input, output, func(reader io.Reader) (adminSetupKeyReader, error) {
		return uv.NewCancelReader(reader)
	})
}

func readAdminSetupKeyWithReader(ctx context.Context, input, output *os.File, newReader func(io.Reader) (adminSetupKeyReader, error)) (key []byte, err error) {
	if ctx.Err() != nil {
		return nil, context.Canceled
	}
	if input == nil || output == nil || !term.IsTerminal(input.Fd()) || !term.IsTerminal(output.Fd()) {
		return nil, errAdminSetupTerminal
	}
	state, stateErr := term.GetState(input.Fd())
	if stateErr != nil {
		return nil, errAdminSetupKeyIO
	}
	// Restore after closing the cancel reader: its Windows implementation also
	// restores console state, which must not overwrite our original state.
	defer func() {
		if term.Restore(input.Fd(), state) != nil {
			err = errAdminSetupKeyIO
		}
		if err != nil {
			clear(key)
			key = nil
		}
	}()
	if _, rawErr := term.MakeRaw(input.Fd()); rawErr != nil {
		return nil, errAdminSetupKeyIO
	}
	reader, readerErr := newReader(input)
	if readerErr != nil {
		return nil, errAdminSetupKeyIO
	}
	defer func() {
		if reader.Close() != nil {
			err = errAdminSetupKeyIO
		}
	}()

	readCtx, cancel := context.WithCancel(ctx)
	chunks := make(chan []byte)
	go readAdminSetupKeyBytes(readCtx, reader, chunks)
	defer func() {
		cancel()
		reader.Cancel()
		// Join the reader before restoring echo, clearing any unconsumed input.
		for chunk := range chunks {
			clear(chunk)
		}
		if _, writeErr := io.WriteString(output, "\x1b[?2004l\r\n"); writeErr != nil {
			err = errAdminSetupKeyIO
		}
	}()
	// Enable paste mode only after raw input and the reader are ready. Never
	// show input bytes, including rejected paste content or parser errors.
	if _, writeErr := io.WriteString(output, "\x1b[?2004hAdmin API key (hidden): "); writeErr != nil {
		return nil, errAdminSetupKeyIO
	}
	return collectAdminSetupKey(ctx, chunks)
}

func readAdminSetupKeyBytes(ctx context.Context, reader io.Reader, chunks chan<- []byte) {
	defer close(chunks)
	for {
		buffer := make([]byte, 4096)
		n, err := reader.Read(buffer)
		if n > 0 {
			select {
			case chunks <- buffer[:n]: // The receiver owns and clears this buffer.
			case <-ctx.Done():
				clear(buffer)
				return
			}
		} else {
			clear(buffer)
		}
		if err != nil {
			return
		}
	}
}

func collectAdminSetupKey(ctx context.Context, chunks <-chan []byte) (key []byte, err error) {
	defer func() {
		if err != nil {
			clear(key)
			key = nil
		}
	}()
	// Recognize only the two bracketed-paste markers. A general terminal event
	// parser can discard invalid bytes or buffer Ctrl+C inside unfinished paste.
	start, end := []byte("\x1b[200~"), []byte("\x1b[201~")
	var sequence [6]byte
	defer clear(sequence[:])
	sequenceLen := 0
	paste, invalid := false, false
	for {
		select {
		case <-ctx.Done():
			return key, context.Canceled
		case chunk, open := <-chunks:
			if !open {
				if ctx.Err() != nil {
					return key, context.Canceled
				}
				return key, errAdminSetupKeyIO
			}
			done := false
			for _, b := range chunk {
				if b == 3 || b == 4 || ctx.Err() != nil {
					err, done = context.Canceled, true
					break
				}
				if sequenceLen > 0 {
					sequence[sequenceLen] = b
					sequenceLen++
					prefix := sequence[:sequenceLen]
					switch {
					case bytes.Equal(prefix, start):
						invalid = invalid || paste
						paste, sequenceLen = true, 0
						clear(sequence[:])
						continue
					case bytes.Equal(prefix, end):
						invalid = invalid || !paste
						paste, sequenceLen = false, 0
						clear(sequence[:])
						continue
					case bytes.HasPrefix(start, prefix) || bytes.HasPrefix(end, prefix):
						continue
					default:
						invalid, sequenceLen = true, 0
						clear(sequence[:])
					}
				}
				switch {
				case b == 0x1b:
					sequence[0], sequenceLen = b, 1
					continue
				case (b == '\r' || b == '\n') && !paste:
					done = true
					if invalid {
						err = errAdminSetupKeyInvalid
					} else if len(key) == 0 {
						err = errAdminSetupKeyEmpty
					}
				case (b == 8 || b == 0x7f) && !paste:
					if len(key) > 0 {
						key[len(key)-1] = 0
						key = key[:len(key)-1]
					}
					continue
				case b <= ' ' || b >= 0x7f:
					invalid = true
				}
				if done {
					break
				}
				if invalid {
					clear(key)
					key = key[:0]
					continue
				}
				if len(key) == cap(key) {
					grown := make([]byte, len(key), max(128, 2*cap(key)))
					copy(grown, key)
					clear(key)
					key = grown
				}
				key = append(key, b)
			}
			clear(chunk)
			if done {
				return key, err
			}
		}
	}
}
