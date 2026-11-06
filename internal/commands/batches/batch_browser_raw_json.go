package batches

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
)

// Format validated stored JSON without loading complete strings or objects.
// Terminal escapes preserve JSON meaning. Downloads retain exact source bytes.
func writeBatchBrowserRawJSON(ctx context.Context, out io.Writer, writeJSON func(io.Writer) error) (err error) {
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		failure := writeJSON(writer)
		_ = writer.CloseWithError(failure)
		done <- failure
	}()
	defer func() {
		_ = reader.CloseWithError(err)
		err = errors.Join(err, <-done)
	}()
	in := bufio.NewReader(batchRecordContextReader{ctx, reader})
	buffer := bufio.NewWriterSize(out, 32*1024)
	depth, quoted, escaped, pending := 0, false, false, false
	newline := func() error {
		// Limit indentation width, not payload size or nesting acceptance.
		_, err := buffer.WriteString("\n" + strings.Repeat("  ", min(depth, 32)))
		return err
	}
	for {
		r, _, readErr := in.ReadRune()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
		if quoted {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '"' {
				quoted = false
			}
			if r >= 0x7f {
				_, err = buffer.WriteString(readable.Text(string(r)))
			} else {
				_, err = buffer.WriteRune(r)
			}
		} else {
			if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
				continue
			}
			closing := r == '}' || r == ']'
			if pending && !closing {
				if err = newline(); err != nil {
					return err
				}
				pending = false
			}
			switch r {
			case '{', '[':
				_, err = buffer.WriteRune(r)
				depth++
				pending = true
			case '}', ']':
				depth--
				if !pending {
					if err = newline(); err != nil {
						return err
					}
				}
				pending = false
				_, err = buffer.WriteRune(r)
			case ',':
				err = buffer.WriteByte(',')
				if err == nil {
					err = newline()
				}
			case ':':
				_, err = buffer.WriteString(": ")
			default:
				quoted = r == '"'
				_, err = buffer.WriteRune(r)
			}
		}
		if err != nil {
			return err
		}
	}
	if err = buffer.WriteByte('\n'); err != nil {
		return err
	}
	return buffer.Flush()
}
