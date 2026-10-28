package batches

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/openai/openai-cli/internal/readable"
)

// This limits retained memory, not the accepted record size.
const batchRecordMemory = 256 * 1024

// A stream retains one validated record. Larger records reuse one private file.
// Matching and interactive navigation retain their separate indexed stores.
type batchRecordStream struct {
	ctx               context.Context
	scanner           batchJSONScanner
	selection, filter string
	current           bool
	visited           bool
	buffer            bytes.Buffer
	file              *os.File
	spilled           bool
	size              int64
	scratch           *batchRecordStore
}

func newBatchRecordStream(ctx context.Context, source io.Reader, selection, filter string) *batchRecordStream {
	s := &batchRecordStream{ctx: ctx, selection: selection, filter: filter}
	s.scanner = batchJSONScanner{ctx: ctx, in: bufio.NewReader(source), out: bufio.NewWriterSize(s, 32*1024)}
	return s
}

func (s *batchRecordStream) temporaryFile() (*os.File, func() error, error) {
	if s.scratch == nil {
		var err error
		s.scratch, err = newBatchScratchStore(s.ctx)
		if err != nil {
			return nil, nil, err
		}
	}
	return s.scratch.temporaryFile()
}

func (s *batchRecordStream) close() error {
	if s.scratch == nil {
		return nil
	}
	return s.scratch.close()
}

func (s *batchRecordStream) Write(data []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if !s.spilled && len(data) <= batchRecordMemory-s.buffer.Len() {
		n, err := s.buffer.Write(data)
		s.size += int64(n)
		return n, err
	}
	if !s.spilled {
		if s.file == nil {
			var err error
			s.file, _, err = s.temporaryFile()
			if err != nil {
				return 0, err
			}
		}
		written, err := s.file.Write(s.buffer.Bytes())
		if err == nil && written != s.buffer.Len() {
			err = io.ErrShortWrite
		}
		if err != nil {
			return 0, batchRecordStorageError(err)
		}
		s.spilled = true
		s.buffer.Reset()
	}
	n, err := s.file.Write(data)
	s.size += int64(n)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return n, batchRecordStorageError(err)
}

func (s *batchRecordStream) reader(start, length int64) io.Reader {
	if s.spilled {
		return batchRecordContextReader{s.ctx, io.NewSectionReader(s.file, start, length)}
	}
	return bytes.NewReader(s.buffer.Bytes()[start : start+length])
}

func (s *batchRecordStream) next() (err error) {
	defer func() {
		err = batchRecordReadError(s.ctx, err)
		if err != nil && s.visited && s.ctx.Err() == nil {
			err = &WorkflowError{message: WorkflowErrorMessage(err) + "\nOutput may be incomplete.", cause: err}
		}
	}()
	s.current = false
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		s.buffer.Reset()
		s.spilled, s.size, s.scanner.position = false, 0, 0
		if s.file != nil {
			if err := s.file.Truncate(0); err != nil {
				return batchRecordStorageError(err)
			}
			if _, err := s.file.Seek(0, io.SeekStart); err != nil {
				return batchRecordStorageError(err)
			}
		}
		s.scanner.out.Reset(s)
		ok, err := s.scanner.nextRecord(s.selection)
		if err != nil || !ok {
			return err
		}
		if s.filter != "" {
			match := batchRecordIDComparison{remaining: s.filter, equal: true}
			if s.scanner.idLength == 0 {
				continue
			}
			if err := readable.DecodeJSONStringBytes(s.ctx, &match, s.reader(s.scanner.idStart, s.scanner.idLength)); err != nil {
				return err
			}
			if !match.equal || match.remaining != "" {
				continue
			}
		}
		s.current = true
		return nil
	}
}

type batchRecordIDComparison struct {
	remaining string
	equal     bool
}

func (c *batchRecordIDComparison) Write(data []byte) (int, error) {
	if len(data) > len(c.remaining) || string(data) != c.remaining[:min(len(data), len(c.remaining))] {
		c.equal = false
	}
	c.remaining = c.remaining[min(len(data), len(c.remaining)):]
	return len(data), nil
}

func (s *batchRecordStream) writeRecord(out io.Writer) error {
	var buffer [32 * 1024]byte
	n, err := io.CopyBuffer(out, s.reader(0, s.size), buffer[:])
	if err == nil && n != s.size {
		return batchRecordIncompleteError()
	}
	return err
}

func (s *batchRecordStream) visit(visit func(RecordItem) error) error {
	kind := "result"
	if s.selection == "input" {
		kind = "input"
	}
	for s.current {
		s.visited = true
		if err := visit(RecordItem{Kind: kind, WriteJSON: s.writeRecord}); err != nil {
			return err
		}
		if err := s.next(); err != nil {
			return err
		}
	}
	return nil
}

func (s *batchRecordStream) writeJSON(out io.Writer, batchID, fileID string) error {
	metadata, _ := json.Marshal(struct {
		BatchID string `json:"batch_id"`
		File    string `json:"file"`
		FileID  string `json:"file_id"`
	}{batchID, s.selection, fileID})
	if _, err := out.Write(metadata[:len(metadata)-1]); err != nil {
		return err
	}
	if _, err := io.WriteString(out, `,"data":[`); err != nil {
		return err
	}
	first := true
	if err := s.visit(func(item RecordItem) error {
		if !first {
			if _, err := io.WriteString(out, ","); err != nil {
				return err
			}
		}
		first = false
		if err := item.WriteJSON(out); err != nil {
			return err
		}
		if flusher, ok := out.(interface{ Flush() error }); ok {
			return flusher.Flush()
		}
		return nil
	}); err != nil {
		return err
	}
	_, err := io.WriteString(out, "]}")
	return err
}

func batchRecordReadError(ctx context.Context, err error) error {
	var workflowErr *WorkflowError
	if err != nil && !errors.As(err, &workflowErr) && ctx.Err() == nil {
		return &WorkflowError{message: "Could not read the batch file completely. Retry results or download the selected file.", cause: err}
	}
	return err
}
