package batches

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/maphash"
	"io"
	"os"
	"unicode/utf8"

	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
)

// Payloads, record links, groups, and hash buckets stay on disk. The hash table
// grows with the record count without retaining an in-memory index. A fresh seed
// prevents producers from choosing pathological bucket chains in advance.
type batchRecordStore struct {
	ctx                               context.Context
	root                              *os.Root
	files                             map[*os.File]batchRecordTemporaryFile
	data, records, groups, buckets    *os.File
	seed                              maphash.Seed
	bucketCount                       uint64
	groupCount, recordCount, position int64
}

type batchStoredGroup struct {
	next, keyStart, keyLength           int64
	inputHead, inputTail, inputCount    int64
	resultHead, resultTail, resultCount int64
}

type batchStoredRecord struct{ start, length, next int64 }

type batchRecordTemporaryFile struct {
	name string
	info os.FileInfo
}

// newBatchScratchStore owns private temporary files without allocating indexes.
func newBatchScratchStore(ctx context.Context) (*batchRecordStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(os.TempDir())
	if err != nil {
		return nil, batchRecordStorageError(err)
	}
	return &batchRecordStore{ctx: ctx, root: root, files: make(map[*os.File]batchRecordTemporaryFile)}, nil
}

func newBatchRecordStore(ctx context.Context) (_ *batchRecordStore, err error) {
	s, err := newBatchScratchStore(ctx)
	if err != nil {
		return nil, err
	}
	s.seed, s.bucketCount = maphash.MakeSeed(), 1024
	defer func() {
		if err != nil {
			err = errors.Join(err, s.close())
		}
	}()
	for _, file := range []**os.File{&s.data, &s.records, &s.groups, &s.buckets} {
		*file, _, err = s.temporaryFile()
		if err != nil {
			return nil, batchRecordStorageError(err)
		}
	}
	if err := s.buckets.Truncate(int64(s.bucketCount) * 8); err != nil {
		return nil, batchRecordStorageError(err)
	}
	return s, nil
}

func batchRecordStorageError(err error) error {
	if err == nil {
		return nil
	}
	return &WorkflowError{message: "Could not prepare batch records in temporary storage. Check temporary storage space and permissions, then retry the same command.", cause: err}
}

func batchRecordIncompleteError() error {
	return &WorkflowError{message: "Temporary batch data is incomplete. Output may be incomplete. Retry the same read-only command.", cause: io.ErrUnexpectedEOF}
}

func (s *batchRecordStore) temporaryFile() (*os.File, func() error, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, nil, err
	}
	name := "openai-batch-records-" + rand.Text()
	file, err := clioutput.CreatePrivateDownloadFile(s.root, name)
	if err != nil {
		return nil, nil, batchRecordStorageError(err)
	}
	info, err := file.Stat()
	s.files[file] = batchRecordTemporaryFile{name: name, info: info}
	if err != nil {
		return nil, nil, errors.Join(batchRecordStorageError(err), s.removeFile(file))
	}
	return file, func() error { return s.removeFile(file) }, nil
}

func (s *batchRecordStore) removeFile(file *os.File) error {
	owned, ok := s.files[file]
	if !ok {
		return nil
	}
	closeErr := file.Close()
	if errors.Is(closeErr, os.ErrClosed) {
		closeErr = nil
	}
	var removeErr error
	if owned.info == nil {
		removeErr = os.ErrInvalid
	} else {
		removeErr = removeBatchDownloadTemporary(s.root, owned.name, owned.info)
	}
	if removeErr == nil {
		delete(s.files, file)
	}
	if err := errors.Join(closeErr, removeErr); err != nil {
		message := "Could not remove all temporary batch records. Check temporary storage permissions before retrying."
		if owned.info != nil && removeErr == os.ErrInvalid {
			message = "A temporary batch record changed its file identity. The replacement was left unchanged. Check temporary storage before retrying."
		}
		return &WorkflowError{message: message, cause: err}
	}
	return nil
}

func (s *batchRecordStore) close() error {
	var err error
	for file := range s.files {
		err = errors.Join(err, s.removeFile(file))
	}
	if s.root != nil {
		if closeErr := s.root.Close(); !errors.Is(closeErr, os.ErrClosed) {
			err = errors.Join(err, batchRecordStorageError(closeErr))
		}
	}
	return err
}

func (s *batchRecordStore) read(file *os.File, data []byte, offset int64) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	_, err := file.ReadAt(data, offset)
	return batchRecordStorageError(err)
}

func (s *batchRecordStore) write(file *os.File, data []byte, offset int64) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	n, err := file.WriteAt(data, offset)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return batchRecordStorageError(err)
}

func (s *batchRecordStore) group(id int64) (batchStoredGroup, error) {
	var data [72]byte
	if err := s.read(s.groups, data[:], (id-1)*72); err != nil {
		return batchStoredGroup{}, err
	}
	var values [9]int64
	for i := range values {
		values[i] = int64(binary.LittleEndian.Uint64(data[i*8:]))
	}
	return batchStoredGroup{values[0], values[1], values[2], values[3], values[4], values[5], values[6], values[7], values[8]}, nil
}

func (s *batchRecordStore) saveGroup(id int64, group batchStoredGroup) error {
	var data [72]byte
	for i, value := range []int64{group.next, group.keyStart, group.keyLength, group.inputHead, group.inputTail, group.inputCount, group.resultHead, group.resultTail, group.resultCount} {
		binary.LittleEndian.PutUint64(data[i*8:], uint64(value))
	}
	return s.write(s.groups, data[:], (id-1)*72)
}

func (s *batchRecordStore) record(id int64) (batchStoredRecord, error) {
	var data [24]byte
	if err := s.read(s.records, data[:], (id-1)*24); err != nil {
		return batchStoredRecord{}, err
	}
	return batchStoredRecord{int64(binary.LittleEndian.Uint64(data[:])), int64(binary.LittleEndian.Uint64(data[8:])), int64(binary.LittleEndian.Uint64(data[16:]))}, nil
}

func (s *batchRecordStore) saveRecord(id int64, record batchStoredRecord) error {
	var data [24]byte
	for i, value := range []int64{record.start, record.length, record.next} {
		binary.LittleEndian.PutUint64(data[i*8:], uint64(value))
	}
	return s.write(s.records, data[:], (id-1)*24)
}

func (s *batchRecordStore) bucket(hash uint64) (int64, error) {
	var data [8]byte
	if err := s.read(s.buckets, data[:], int64(hash&(s.bucketCount-1))*8); err != nil {
		return 0, err
	}
	return int64(binary.LittleEndian.Uint64(data[:])), nil
}

func (s *batchRecordStore) saveBucket(hash uint64, group int64) error {
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], uint64(group))
	return s.write(s.buckets, data[:], int64(hash&(s.bucketCount-1))*8)
}

func (s *batchRecordStore) equalKey(group batchStoredGroup, key string) (bool, error) {
	if int64(len(key)) != group.keyLength {
		return false, nil
	}
	var buffer [4096]byte
	for offset := int64(0); offset < group.keyLength; {
		n := min(int64(len(buffer)), group.keyLength-offset)
		if err := s.read(s.data, buffer[:n], group.keyStart+offset); err != nil {
			return false, err
		}
		if string(buffer[:n]) != key[offset:offset+n] {
			return false, nil
		}
		offset += n
	}
	return true, nil
}

func (s *batchRecordStore) equalStoredKeys(group batchStoredGroup, start, length int64) (bool, error) {
	if length != group.keyLength {
		return false, nil
	}
	var left, right [4096]byte
	for offset := int64(0); offset < length; {
		n := min(int64(len(left)), length-offset)
		if err := s.read(s.data, left[:n], group.keyStart+offset); err != nil {
			return false, err
		}
		if err := s.read(s.data, right[:n], start+offset); err != nil {
			return false, err
		}
		if !bytes.Equal(left[:n], right[:n]) {
			return false, nil
		}
		offset += n
	}
	return true, nil
}

func (s *batchRecordStore) normalizeID(start, length int64) (int64, int64, uint64, error) {
	keyStart := s.position
	if _, err := s.data.Seek(keyStart, io.SeekStart); err != nil {
		return 0, 0, 0, batchRecordStorageError(err)
	}
	var hash maphash.Hash
	hash.SetSeed(s.seed)
	err := readable.DecodeJSONStringBytes(s.ctx, io.MultiWriter(s.data, &hash), io.NewSectionReader(s.data, start, length))
	if err != nil {
		return 0, 0, 0, batchRecordStorageError(err)
	}
	position, err := s.data.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, 0, 0, batchRecordStorageError(err)
	}
	s.position = position
	return keyStart, position - keyStart, hash.Sum64(), nil
}

func (s *batchRecordStore) grow() error {
	if uint64(s.groupCount) < s.bucketCount*2 {
		return nil
	}
	s.bucketCount *= 2
	if err := s.buckets.Truncate(0); err != nil {
		return batchRecordStorageError(err)
	}
	if err := s.buckets.Truncate(int64(s.bucketCount) * 8); err != nil {
		return batchRecordStorageError(err)
	}
	var buffer [32 * 1024]byte
	for id := int64(1); id <= s.groupCount; id++ {
		group, err := s.group(id)
		if err != nil {
			return err
		}
		if group.keyLength == 0 {
			continue
		}
		var hash maphash.Hash
		hash.SetSeed(s.seed)
		if _, err := io.CopyBuffer(&hash, batchRecordContextReader{s.ctx, io.NewSectionReader(s.data, group.keyStart, group.keyLength)}, buffer[:]); err != nil {
			return batchRecordStorageError(err)
		}
		group.next, err = s.bucket(hash.Sum64())
		if err != nil {
			return err
		}
		if err := s.saveGroup(id, group); err != nil {
			return err
		}
		if err := s.saveBucket(hash.Sum64(), id); err != nil {
			return err
		}
	}
	return nil
}

func (s *batchRecordStore) add(keyStart, keyLength int64, hash uint64, start, length int64, input, match bool) error {
	if !match {
		s.recordCount++
		return s.saveRecord(s.recordCount, batchStoredRecord{start: start, length: length})
	}
	var group batchStoredGroup
	var id int64
	if match && keyLength != 0 {
		var err error
		id, err = s.bucket(hash)
		if err != nil {
			return err
		}
		for id != 0 {
			group, err = s.group(id)
			if err != nil {
				return err
			}
			equal, err := s.equalStoredKeys(group, keyStart, keyLength)
			if err != nil {
				return err
			}
			if equal {
				break
			}
			id = group.next
		}
	}
	if id == 0 {
		group = batchStoredGroup{keyStart: keyStart, keyLength: keyLength}
		s.groupCount++
		id = s.groupCount
		if match && keyLength != 0 {
			var err error
			group.next, err = s.bucket(hash)
			if err != nil {
				return err
			}
			if err := s.saveBucket(hash, id); err != nil {
				return err
			}
		}
	}
	s.recordCount++
	if err := s.saveRecord(s.recordCount, batchStoredRecord{start: start, length: length}); err != nil {
		return err
	}
	head, tail, count := &group.resultHead, &group.resultTail, &group.resultCount
	if input {
		head, tail, count = &group.inputHead, &group.inputTail, &group.inputCount
	}
	if *tail != 0 {
		previous, err := s.record(*tail)
		if err != nil {
			return err
		}
		previous.next = s.recordCount
		if err := s.saveRecord(*tail, previous); err != nil {
			return err
		}
	} else {
		*head = s.recordCount
	}
	*tail = s.recordCount
	*count++
	if err := s.saveGroup(id, group); err != nil {
		return err
	}
	if match {
		return s.grow()
	}
	return nil
}

func (s *batchRecordStore) readJSONL(source io.Reader, selection, filter string, input, match bool) error {
	if _, err := s.data.Seek(s.position, io.SeekStart); err != nil {
		return batchRecordStorageError(err)
	}
	scanner := batchJSONScanner{ctx: s.ctx, in: bufio.NewReader(source), out: bufio.NewWriterSize(s.data, 32*1024), position: s.position}
	for line := 1; ; line++ {
		err := scanner.outerSpace()
		if errors.Is(err, io.EOF) {
			return s.ctx.Err()
		}
		if err != nil {
			return err
		}
		b, err := scanner.peek()
		if err != nil {
			return err
		}
		if b == '\n' {
			if _, err := scanner.take(false); err != nil {
				return err
			}
			continue
		}
		start := scanner.position
		scanner.idStart, scanner.idLength, scanner.idFields = 0, 0, 0
		if b != '{' {
			err = errBatchRecordJSON
		} else {
			err = scanner.value(0)
		}
		if errors.Is(err, errBatchRecordID) {
			return batchError(fmt.Sprintf("The %s file repeats custom_id on line %d. Download the file to inspect the ambiguous record.", selection, line))
		}
		if err != nil {
			if errors.Is(err, errBatchRecordJSON) || errors.Is(err, io.EOF) {
				return invalidBatchRecordLine(selection, line)
			}
			return err
		}
		if err := scanner.out.Flush(); err != nil {
			return batchRecordStorageError(err)
		}
		s.position = scanner.position
		keyStart, keyLength, hash := int64(0), int64(0), uint64(0)
		if (filter != "" || match) && scanner.idLength != 0 {
			var err error
			keyStart, keyLength, hash, err = s.normalizeID(scanner.idStart, scanner.idLength)
			if err != nil {
				return err
			}
		}
		selected := true
		if filter != "" {
			selected, err = s.equalKey(batchStoredGroup{keyStart: keyStart, keyLength: keyLength}, filter)
			if err != nil {
				return err
			}
		}
		if selected {
			if err := s.add(keyStart, keyLength, hash, start, scanner.position-start, input, match); err != nil {
				return err
			}
		} else {
			if err := s.data.Truncate(start); err != nil {
				return batchRecordStorageError(err)
			}
			s.position = start
		}
		if _, err := s.data.Seek(s.position, io.SeekStart); err != nil {
			return batchRecordStorageError(err)
		}
		scanner.position = s.position
		err = scanner.outerSpace()
		if errors.Is(err, io.EOF) {
			return s.ctx.Err()
		}
		if err != nil {
			return err
		}
		b, err = scanner.take(false)
		if err != nil {
			return err
		}
		if b != '\n' {
			return invalidBatchRecordLine(selection, line)
		}
	}
}

func invalidBatchRecordLine(selection string, line int) error {
	return batchError(fmt.Sprintf("The %s file contains invalid JSONL on line %d. Each nonempty line must contain one JSON object. Download the file to inspect it.", selection, line))
}

func (s *batchRecordStore) writeRecord(out io.Writer, id int64) error {
	record, err := s.record(id)
	if err != nil {
		return err
	}
	var buffer [32 * 1024]byte
	written, err := io.CopyBuffer(out, batchRecordContextReader{s.ctx, io.NewSectionReader(s.data, record.start, record.length)}, buffer[:])
	if err == nil && written != record.length {
		return batchRecordIncompleteError()
	}
	return err
}

func (s *batchRecordStore) writeLinkedRecords(out io.Writer, id int64) error {
	first := true
	for id != 0 {
		if !first {
			if _, err := io.WriteString(out, ","); err != nil {
				return err
			}
		}
		first = false
		if err := s.writeRecord(out, id); err != nil {
			return err
		}
		record, err := s.record(id)
		if err != nil {
			return err
		}
		id = record.next
	}
	return nil
}

func (s *batchRecordStore) writeKey(out io.Writer, group batchStoredGroup) error {
	if _, err := io.WriteString(out, `"`); err != nil {
		return err
	}
	reader := bufio.NewReader(batchRecordContextReader{s.ctx, io.NewSectionReader(s.data, group.keyStart, group.keyLength)})
	var encodedRune [utf8.UTFMax]byte
	for {
		value, _, err := reader.ReadRune()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if value >= 0x20 && value != '"' && value != '\\' && value != '<' && value != '>' && value != '&' && value != '\u2028' && value != '\u2029' {
			size := utf8.EncodeRune(encodedRune[:], value)
			if _, err := out.Write(encodedRune[:size]); err != nil {
				return err
			}
		} else {
			encoded, _ := json.Marshal(string(value))
			if _, err := out.Write(encoded[1 : len(encoded)-1]); err != nil {
				return err
			}
		}
	}
	_, err := io.WriteString(out, `"`)
	return err
}

func (s *batchRecordStore) writeGroup(out io.Writer, id int64) error {
	group, err := s.group(id)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(out, "{"); err != nil {
		return err
	}
	if group.keyLength != 0 {
		if _, err := io.WriteString(out, `"custom_id":`); err != nil {
			return err
		}
		if err := s.writeKey(out, group); err != nil {
			return err
		}
		if _, err := io.WriteString(out, ","); err != nil {
			return err
		}
	}
	status, issues := describeBatchRecordCounts(group.keyLength != 0, group.inputCount, group.resultCount)
	metadata, _ := json.Marshal(struct {
		Match  string   `json:"match"`
		Issues []string `json:"issues,omitempty"`
	}{status, issues})
	if _, err := out.Write(metadata[1 : len(metadata)-1]); err != nil {
		return err
	}
	if _, err := io.WriteString(out, `,"inputs":[`); err != nil {
		return err
	}
	if err := s.writeLinkedRecords(out, group.inputHead); err != nil {
		return err
	}
	if _, err := io.WriteString(out, `],"results":[`); err != nil {
		return err
	}
	if err := s.writeLinkedRecords(out, group.resultHead); err != nil {
		return err
	}
	_, err = io.WriteString(out, "]}")
	return err
}

func (s *batchRecordStore) writeJSON(out io.Writer, batchID, selection, fileID string, match bool) error {
	buffer := bufio.NewWriterSize(out, 32*1024)
	metadata, err := json.Marshal(struct {
		BatchID string `json:"batch_id"`
		File    string `json:"file"`
		FileID  string `json:"file_id"`
	}{batchID, selection, fileID})
	if err != nil {
		return err
	}
	if _, err := buffer.Write(metadata[:len(metadata)-1]); err != nil {
		return err
	}
	if _, err := buffer.WriteString(`,"data":[`); err != nil {
		return err
	}
	count := s.recordCount
	if match {
		count = s.groupCount
	}
	for id := int64(1); id <= count; id++ {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if id > 1 {
			if err := buffer.WriteByte(','); err != nil {
				return err
			}
		}
		if match {
			err = s.writeGroup(buffer, id)
		} else {
			err = s.writeRecord(buffer, id)
		}
		if err != nil {
			return err
		}
	}
	if _, err := buffer.WriteString("]}"); err != nil {
		return err
	}
	return buffer.Flush()
}

func describeBatchRecordCounts(hasID bool, inputs, results int64) (string, []string) {
	status := "matched"
	var issues []string
	switch {
	case !hasID:
		status = "missing_custom_id"
		if inputs > 0 {
			issues = []string{"missing_input_custom_id"}
		} else {
			issues = []string{"missing_result_custom_id"}
		}
	case inputs == 0:
		status, issues = "missing_input", []string{"missing_input"}
	case results == 0:
		status, issues = "missing_result", []string{"missing_result"}
	}
	if hasID && inputs > 1 {
		status = "ambiguous"
		issues = append(issues, "duplicate_input_custom_id")
	}
	if hasID && results > 1 {
		status = "ambiguous"
		issues = append(issues, "duplicate_result_custom_id")
	}
	return status, issues
}
