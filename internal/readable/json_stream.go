package readable

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// WriteJSONStream renders one already validated JSON document with Write's
// labels and spacing. Strings and numbers stream in bounded chunks. Unusually
// large keys use caller-owned private scratch storage, never a rejection limit.
func WriteJSONStream(ctx context.Context, out io.Writer, source io.Reader, temporaryFile func() (*os.File, func() error, error)) error {
	buffer := bufio.NewWriterSize(out, 32*1024)
	r := jsonTextReader{ctx: ctx, in: bufio.NewReader(source), p: printer{w: buffer}, flush: buffer.Flush, temporaryFile: temporaryFile}
	err := r.value(0, "")
	if r.pendingKey != nil {
		err = errors.Join(err, r.pendingKey.close())
	}
	return errors.Join(err, r.p.err, buffer.Flush(), ctx.Err())
}

type jsonTextReader struct {
	ctx           context.Context
	in            *bufio.Reader
	p             printer
	flush         func() error
	read          int
	temporaryFile func() (*os.File, func() error, error)
	pendingKey    *jsonStreamKey
}

func (r *jsonTextReader) byte() (byte, error) {
	if r.read%4096 == 0 {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
	}
	if r.p.err != nil {
		return 0, r.p.err
	}
	b, err := r.in.ReadByte()
	r.read++
	return b, err
}

func (r *jsonTextReader) peek() (byte, error) {
	for {
		// Expose completed output before waiting for the next source chunk.
		// Keep buffering while input remains, including nested record fields.
		if r.in.Buffered() == 0 && r.flush != nil {
			if err := r.flush(); err != nil {
				return 0, err
			}
		}
		data, err := r.in.Peek(1)
		if err != nil {
			return 0, err
		}
		if !strings.ContainsRune(" \r\n\t", rune(data[0])) {
			return data[0], nil
		}
		if _, err := r.byte(); err != nil {
			return 0, err
		}
	}
}

func (r *jsonTextReader) expect(want byte) error {
	b, err := r.byte()
	if err != nil {
		return err
	}
	if b != want {
		return fmt.Errorf("invalid JSON in readable stream")
	}
	return nil
}

func (r *jsonTextReader) hexRune() (rune, error) {
	var value rune
	for range 4 {
		b, err := r.byte()
		if err != nil {
			return 0, err
		}
		value *= 16
		switch {
		case b >= '0' && b <= '9':
			value += rune(b - '0')
		case b >= 'a' && b <= 'f':
			value += rune(b - 'a' + 10)
		case b >= 'A' && b <= 'F':
			value += rune(b - 'A' + 10)
		default:
			return 0, fmt.Errorf("invalid JSON string escape")
		}
	}
	return value, nil
}

func (r *jsonTextReader) stringRune() (rune, bool, error) {
	b, err := r.byte()
	if err != nil {
		return 0, false, err
	}
	if b == '"' {
		return 0, true, nil
	}
	if b >= utf8.RuneSelf {
		if err := r.in.UnreadByte(); err != nil {
			return 0, false, err
		}
		value, _, err := r.in.ReadRune()
		return value, false, err
	}
	if b != '\\' {
		return rune(b), false, nil
	}
	b, err = r.byte()
	if err != nil {
		return 0, false, err
	}
	switch b {
	case '"', '/', '\\':
		return rune(b), false, nil
	case 'b':
		return '\b', false, nil
	case 'f':
		return '\f', false, nil
	case 'n':
		return '\n', false, nil
	case 'r':
		return '\r', false, nil
	case 't':
		return '\t', false, nil
	case 'u':
		value, err := r.hexRune()
		if err != nil {
			return 0, false, err
		}
		if utf16.IsSurrogate(value) {
			// A lone surrogate can end the record. Only wait for four more
			// digits when the next bytes actually start a Unicode escape.
			for i, want := range []byte(`\u`) {
				prefix, err := r.in.Peek(i + 1)
				if err != nil {
					return 0, false, err
				}
				if prefix[i] != want {
					return utf8.RuneError, false, nil
				}
			}
			// Preserve gjson's existing treatment of adjacent surrogate escapes.
			// It consumes the next Unicode escape even for an invalid pair.
			for range 2 {
				if _, err := r.byte(); err != nil {
					return 0, false, err
				}
			}
			next, err := r.hexRune()
			if err != nil {
				return 0, false, err
			}
			return utf16.DecodeRune(value, next), false, nil
		}
		return value, false, nil
	}
	return 0, false, fmt.Errorf("invalid JSON string escape")
}

// Preserve raw bytes for identifiers and field names. Scalar values instead
// pass through stringRune and the existing readable sanitizer.
func (r *jsonTextReader) invalidUTF8Byte() (byte, bool, error) {
	data, _ := r.in.Peek(utf8.UTFMax)
	if len(data) > 0 && data[0] >= utf8.RuneSelf {
		if _, size := utf8.DecodeRune(data); size == 1 {
			value, err := r.byte()
			return value, true, err
		}
	}
	return 0, false, nil
}

// DecodeJSONStringBytes decodes escapes from an already validated JSON string.
// Raw invalid UTF8 bytes retain their identity. Surrogate escapes preserve
// existing gjson behavior. Readable values separately sanitize invalid bytes.
func DecodeJSONStringBytes(ctx context.Context, out io.Writer, source io.Reader) error {
	r := jsonTextReader{ctx: ctx, in: bufio.NewReader(source)}
	if err := r.expect('"'); err != nil {
		return err
	}
	buffer := bufio.NewWriterSize(out, 32*1024)
	for {
		if value, invalid, err := r.invalidUTF8Byte(); err != nil {
			return err
		} else if invalid {
			if err := buffer.WriteByte(value); err != nil {
				return err
			}
			continue
		}
		value, end, err := r.stringRune()
		if err != nil {
			return err
		}
		if end {
			return buffer.Flush()
		}
		if _, err := buffer.WriteRune(value); err != nil {
			return err
		}
	}
}

func (r *jsonTextReader) key() (_ *jsonStreamKey, err error) {
	key := &jsonStreamKey{canonical: true}
	defer func() {
		if err != nil {
			err = errors.Join(err, key.close())
		}
	}()
	if err := r.expect('"'); err != nil {
		return nil, err
	}
	var chunk strings.Builder
	wordStart := true
	for {
		if raw, invalid, err := r.invalidUTF8Byte(); err != nil {
			return nil, err
		} else if invalid {
			key.canonical = false
			wordStart = false
			chunk.WriteByte(raw)
		} else {
			value, end, err := r.stringRune()
			if err != nil {
				return nil, err
			}
			if end {
				break
			}
			if value == '_' {
				if wordStart {
					key.canonical = false
				}
				wordStart = true
			} else {
				if wordStart && (value < 'a' || value > 'z') {
					key.canonical = false
				}
				if !(value >= 'a' && value <= 'z' || value >= '0' && value <= '9') {
					key.canonical = false
				}
				wordStart = false
			}
			chunk.WriteRune(value)
		}
		if chunk.Len() >= 4096 {
			if key.file == nil {
				if r.temporaryFile == nil {
					return nil, errors.New("Readable output needs temporary storage for a large field name.")
				}
				key.file, key.cleanup, err = r.temporaryFile()
				if err != nil {
					return nil, err
				}
			}
			if _, err := io.WriteString(key.file, chunk.String()); err != nil {
				return nil, &JSONStreamStorageError{cause: err}
			}
			chunk.Reset()
		}
	}
	key.canonical = key.canonical && !wordStart
	if key.file == nil {
		key.text = chunk.String()
	} else {
		if _, err := io.WriteString(key.file, chunk.String()); err != nil {
			return nil, &JSONStreamStorageError{cause: err}
		}
		if _, err := key.file.Seek(0, io.SeekStart); err != nil {
			return nil, &JSONStreamStorageError{cause: err}
		}
	}
	return key, nil
}

func (r *jsonTextReader) prefix(depth int, prefix string, key *jsonStreamKey, trim bool) error {
	r.p.write(strings.Repeat("  ", depth))
	if r.p.err != nil {
		return r.p.err
	}
	if key != nil {
		err := key.write(r.ctx, r.p.w)
		err = errors.Join(err, key.close())
		if err != nil {
			return err
		}
	}
	if trim {
		prefix = strings.TrimSuffix(prefix, " ")
	}
	r.p.write(prefix)
	return r.p.err
}

func (r *jsonTextReader) scalar(depth int, prefix string, key *jsonStreamKey) error {
	indent := strings.Repeat("  ", depth)
	continuation := indent
	if prefix != "" {
		continuation += "  "
	}
	if err := r.prefix(depth, prefix, key, false); err != nil {
		return err
	}
	b, err := r.peek()
	if err != nil {
		return err
	}
	if b != '"' {
		if b == 'n' {
			for _, want := range []byte("null") {
				if err := r.expect(want); err != nil {
					return err
				}
			}
			r.p.write("(null)\n")
			return r.p.err
		}
		var chunk strings.Builder
		for {
			data, err := r.in.Peek(1)
			if errors.Is(err, io.EOF) || err == nil && strings.ContainsRune(",]} \n\r\t", rune(data[0])) {
				break
			}
			if err != nil {
				return err
			}
			b, err := r.byte()
			if err != nil {
				return err
			}
			chunk.WriteByte(b)
			if chunk.Len() >= 4096 {
				r.p.write(chunk.String())
				chunk.Reset()
			}
		}
		r.p.write(chunk.String() + "\n")
		return r.p.err
	}
	if err := r.expect('"'); err != nil {
		return err
	}
	var chunk strings.Builder
	empty := true
	for {
		value, end, err := r.stringRune()
		if err != nil {
			return err
		}
		if end {
			break
		}
		empty = false
		if value == '\n' {
			r.p.write(Text(chunk.String()) + "\n" + continuation)
			chunk.Reset()
		} else {
			chunk.WriteRune(value)
		}
		if chunk.Len() >= 4096 {
			r.p.write(Text(chunk.String()))
			chunk.Reset()
		}
	}
	if empty {
		r.p.write("(empty string)")
	} else {
		r.p.write(Text(chunk.String()))
	}
	r.p.write("\n")
	return r.p.err
}

func (r *jsonTextReader) value(depth int, prefix string) (err error) {
	key := r.pendingKey
	r.pendingKey = nil
	if key != nil {
		defer func() { err = errors.Join(err, key.close()) }()
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	b, err := r.peek()
	if err != nil {
		return err
	}
	if b != '{' && b != '[' {
		return r.scalar(depth, prefix, key)
	}
	object := b == '{'
	end := byte(']')
	if object {
		end = '}'
	}
	if _, err := r.byte(); err != nil {
		return err
	}
	b, err = r.peek()
	if err != nil {
		return err
	}
	if b == end {
		text := "(empty list)"
		if object {
			text = "(empty object)"
		}
		if err := r.prefix(depth, prefix, key, false); err != nil {
			return err
		}
		r.p.write(text + "\n")
		return r.expect(end)
	}
	if prefix != "" {
		if err := r.prefix(depth, prefix, key, true); err != nil {
			return err
		}
		r.p.write("\n")
		depth++
	}
	previousRecord := false
	for index := 1; ; index++ {
		prefix := strconv.Itoa(index) + ". "
		if object {
			key, err := r.key()
			if err != nil {
				return err
			}
			r.pendingKey = key
			prefix = ": "
			if _, err := r.peek(); err != nil {
				return err
			}
			if err := r.expect(':'); err != nil {
				return err
			}
		}
		b, err := r.peek()
		if err != nil {
			return err
		}
		record := b == '{' || b == '['
		if !object && index > 1 && (record || previousRecord) {
			r.p.write("\n")
		}
		if err := r.value(depth, prefix); err != nil {
			return err
		}
		previousRecord = record
		b, err = r.peek()
		if err != nil {
			return err
		}
		if b == end {
			return r.expect(end)
		}
		if err := r.expect(','); err != nil {
			return err
		}
		if _, err := r.peek(); err != nil {
			return err
		}
	}
}
