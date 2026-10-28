package batches

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// batchJSONScanner validates one JSONL object at a time. Payload strings and
// numbers pass directly to the private spool. IDs also remain on disk.
// The depth limit matches encoding/json's existing limit; there is no byte limit.
type batchJSONScanner struct {
	ctx               context.Context
	in                *bufio.Reader
	out               *bufio.Writer
	position          int64
	consumed          int64
	idStart, idLength int64
	idFields          int
	urlCapture        int // Opt-in bounded capture for submit endpoint inference.
	url               string
	urlFields         int
}

var errBatchRecordJSON = errors.New("invalid batch JSON record")
var errBatchRecordID = errors.New("duplicate batch custom_id")
var errBatchRecordURL = errors.New("invalid batch url")
var errBatchRecordDuplicateURL = errors.New("duplicate batch url")

func (s *batchJSONScanner) peek() (byte, error) {
	if s.consumed%4096 == 0 {
		if err := s.ctx.Err(); err != nil {
			return 0, err
		}
	}
	data, err := s.in.Peek(1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

func (s *batchJSONScanner) take(copy bool) (byte, error) {
	if _, err := s.peek(); err != nil {
		return 0, err
	}
	b, err := s.in.ReadByte()
	if err != nil {
		return 0, err
	}
	s.consumed++
	if copy {
		if err := s.out.WriteByte(b); err != nil {
			return 0, batchRecordStorageError(err)
		}
		s.position++
	}
	return b, nil
}

func (s *batchJSONScanner) expect(want byte) error {
	b, err := s.take(true)
	if err != nil {
		return err
	}
	if b != want {
		return errBatchRecordJSON
	}
	return nil
}

func (s *batchJSONScanner) space() error {
	for {
		b, err := s.peek()
		if err != nil {
			return err
		}
		if b != ' ' && b != '\t' && b != '\r' {
			return nil
		}
		if _, err := s.take(false); err != nil {
			return err
		}
	}
}

// Preserve the previous TrimSpace behavior outside each JSONL record.
func (s *batchJSONScanner) outerSpace() error {
	for {
		b, err := s.peek()
		if err != nil {
			return err
		}
		if b == '\n' {
			return nil
		}
		if b < utf8.RuneSelf {
			if !unicode.IsSpace(rune(b)) {
				return nil
			}
			if _, err := s.take(false); err != nil {
				return err
			}
			continue
		}
		data, _ := s.in.Peek(utf8.UTFMax)
		r, size := utf8.DecodeRune(data)
		if !unicode.IsSpace(r) {
			return nil
		}
		for range size {
			if _, err := s.take(false); err != nil {
				return err
			}
		}
	}
}

// A positive capture value bounds inspected string bytes. Other values skip
// capture. Longer strings still stream completely, returning an empty value.
func (s *batchJSONScanner) stringValue(capture int) (string, error) {
	var value strings.Builder
	write := func(b byte) {
		if capture > 0 && value.Len() <= capture {
			value.WriteByte(b)
		}
	}
	b, err := s.take(true)
	if err != nil {
		return "", err
	}
	if b != '"' {
		return "", errBatchRecordJSON
	}
	write(b)
	for {
		b, err = s.take(true)
		if err != nil {
			return "", err
		}
		write(b)
		if b == '"' {
			break
		}
		if b < 0x20 {
			return "", errBatchRecordJSON
		}
		if b != '\\' {
			continue
		}
		b, err = s.take(true)
		if err != nil {
			return "", err
		}
		write(b)
		if strings.ContainsRune(`"\/bfnrt`, rune(b)) {
			continue
		}
		if b != 'u' {
			return "", errBatchRecordJSON
		}
		for range 4 {
			b, err = s.take(true)
			if err != nil {
				return "", err
			}
			write(b)
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F') {
				return "", errBatchRecordJSON
			}
		}
	}
	if capture <= 0 || value.Len() > capture {
		return "", nil
	}
	var decoded string
	if err := json.Unmarshal([]byte(value.String()), &decoded); err != nil {
		return "", errBatchRecordJSON
	}
	return decoded, nil
}

func (s *batchJSONScanner) digits(required bool) error {
	count := 0
	for {
		b, err := s.peek()
		if errors.Is(err, io.EOF) && (!required || count > 0) {
			return nil
		}
		if err != nil {
			return err
		}
		if b < '0' || b > '9' {
			break
		}
		if _, err := s.take(true); err != nil {
			return err
		}
		count++
	}
	if required && count == 0 {
		return errBatchRecordJSON
	}
	return nil
}

func (s *batchJSONScanner) number() error {
	b, err := s.peek()
	if err != nil {
		return err
	}
	if b == '-' {
		if _, err := s.take(true); err != nil {
			return err
		}
		b, err = s.peek()
		if err != nil {
			return err
		}
	}
	if b == '0' {
		_, err = s.take(true)
	} else {
		err = s.digits(true)
	}
	if err != nil {
		return err
	}
	b, err = s.peek()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	if b == '.' {
		if _, err := s.take(true); err != nil {
			return err
		}
		if err := s.digits(true); err != nil {
			return err
		}
		b, err = s.peek()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	if b == 'e' || b == 'E' {
		if _, err := s.take(true); err != nil {
			return err
		}
		b, err = s.peek()
		if err != nil {
			return err
		}
		if b == '+' || b == '-' {
			if _, err := s.take(true); err != nil {
				return err
			}
		}
		return s.digits(true)
	}
	return nil
}

func (s *batchJSONScanner) value(depth int) error {
	if err := s.space(); err != nil {
		return err
	}
	b, err := s.peek()
	if err != nil {
		return err
	}
	switch b {
	case '{', '[':
		if depth >= 10000 {
			return errBatchRecordJSON
		}
		object := b == '{'
		end := byte(']')
		if object {
			end = '}'
		}
		if _, err := s.take(true); err != nil {
			return err
		}
		if err := s.space(); err != nil {
			return err
		}
		if b, err = s.peek(); err != nil {
			return err
		} else if b == end {
			return s.expect(end)
		}
		for {
			key := ""
			if object {
				key, err = s.stringValue(64)
				if err != nil {
					return err
				}
				if err := s.space(); err != nil {
					return err
				}
				if err := s.expect(':'); err != nil {
					return err
				}
				if err := s.space(); err != nil {
					return err
				}
			}
			if depth == 0 && object && key == "url" && s.urlCapture > 0 {
				s.urlFields++
				if s.urlFields > 1 {
					return errBatchRecordDuplicateURL
				}
				if b, err = s.peek(); err != nil {
					return err
				} else if b != '"' {
					return errBatchRecordURL
				}
				s.url, err = s.stringValue(s.urlCapture)
			} else if depth == 0 && object && key == "custom_id" {
				s.idFields++
				if s.idFields > 1 {
					return errBatchRecordID
				}
				if b, err = s.peek(); err != nil {
					return err
				} else if b == '"' {
					s.idStart = s.position
					_, err = s.stringValue(-1)
					s.idLength = s.position - s.idStart
				} else {
					err = s.value(depth + 1)
				}
			} else {
				err = s.value(depth + 1)
			}
			if err != nil {
				return err
			}
			if err := s.space(); err != nil {
				return err
			}
			b, err = s.peek()
			if err != nil {
				return err
			}
			if b == end {
				return s.expect(end)
			}
			if err := s.expect(','); err != nil {
				return err
			}
			if err := s.space(); err != nil {
				return err
			}
		}
	case '"':
		_, err := s.stringValue(-1)
		return err
	case 'n', 't', 'f':
		word := "null"
		if b == 't' {
			word = "true"
		} else if b == 'f' {
			word = "false"
		}
		for i := range word {
			if err := s.expect(word[i]); err != nil {
				return err
			}
		}
		return nil
	default:
		return s.number()
	}
}
