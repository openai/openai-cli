package readable

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// JSONStreamStorageError identifies scratch storage failures without exposing paths.
type JSONStreamStorageError struct{ cause error }

func (e *JSONStreamStorageError) Error() string {
	return "Could not use temporary storage for readable output. Check temporary storage space and permissions, then retry."
}
func (e *JSONStreamStorageError) Unwrap() error { return e.cause }

// A label spills after a small working buffer; this does not limit accepted keys.
// The caller supplies private files and retains ownership if removal fails.
type jsonStreamKey struct {
	text      string
	file      *os.File
	cleanup   func() error
	canonical bool
}

func (k *jsonStreamKey) close() error {
	if k.cleanup == nil {
		return nil
	}
	cleanup := k.cleanup
	k.cleanup = nil
	return cleanup()
}

type jsonStreamContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r jsonStreamContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(data)
}

func (k *jsonStreamKey) write(ctx context.Context, out io.Writer) error {
	if k.file == nil {
		_, err := io.WriteString(out, Text(label(k.text)))
		return err
	}
	in := bufio.NewReader(jsonStreamContextReader{ctx, k.file})
	buffer := bufio.NewWriterSize(out, 4096)
	if k.canonical {
		if err := k.writeCanonical(in, buffer); err != nil {
			return err
		}
	} else {
		if err := buffer.WriteByte('"'); err != nil {
			return err
		}
		for {
			value, size, err := in.ReadRune()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return &JSONStreamStorageError{cause: err}
			}
			if value == utf8.RuneError && size == 1 {
				if err := in.UnreadRune(); err != nil {
					return &JSONStreamStorageError{cause: err}
				}
				value, err := in.ReadByte()
				if err != nil {
					return &JSONStreamStorageError{cause: err}
				}
				const hex = "0123456789abcdef"
				encoded := [4]byte{'\\', 'x', hex[value>>4], hex[value&15]}
				if _, err := buffer.Write(encoded[:]); err != nil {
					return err
				}
				continue
			}
			if strconv.IsPrint(value) && value != '\\' && value != '"' {
				if _, err := buffer.WriteRune(value); err != nil {
					return err
				}
			} else {
				encoded := strconv.Quote(string(value))
				if _, err := buffer.WriteString(encoded[1 : len(encoded)-1]); err != nil {
					return err
				}
			}
		}
		if err := buffer.WriteByte('"'); err != nil {
			return err
		}
	}
	return buffer.Flush()
}

func (k *jsonStreamKey) writeCanonical(in *bufio.Reader, out *bufio.Writer) error {
	var word [5]byte
	length := 0
	streamed, first := false, true
	for {
		b, err := in.ReadByte()
		if err != nil && !errors.Is(err, io.EOF) {
			return &JSONStreamStorageError{cause: err}
		}
		if errors.Is(err, io.EOF) || b == '_' {
			if !streamed {
				value := string(word[:length])
				switch value {
				case "id", "api", "url", "json":
					value = strings.ToUpper(value)
				case "ids", "urls":
					value = strings.ToUpper(value[:len(value)-1]) + "s"
				default:
					if first && len(value) > 0 {
						value = strings.ToUpper(value[:1]) + value[1:]
					}
				}
				if _, err := out.WriteString(value); err != nil {
					return err
				}
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err := out.WriteByte(' '); err != nil {
				return err
			}
			length, streamed, first = 0, false, false
			continue
		}
		if streamed {
			if err := out.WriteByte(b); err != nil {
				return err
			}
			continue
		}
		word[length] = b
		length++
		if length == len(word) {
			if first {
				word[0] -= 'a' - 'A'
			}
			if _, err := out.Write(word[:]); err != nil {
				return err
			}
			streamed = true
		}
	}
}
