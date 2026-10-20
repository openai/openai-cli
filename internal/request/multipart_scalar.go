package request

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// MultipartScalarError retains a scalar read failure behind its declared flag name.
type MultipartScalarError struct {
	message string
	cause   error
}

func (e *MultipartScalarError) Error() string { return e.message }
func (e *MultipartScalarError) Unwrap() error { return e.cause }

// ReadMultipartScalar inspects scalar @file references that arrive as upload readers after shared trusted-input
// expansion. Inspect their contents once and replay the exact original bytes,
// retaining multipart filenames/content types. Source image and mask readers
// never enter this path and stay streamed. There is no new file-size limit.
func ReadMultipartScalar(ctx context.Context, name string, value any) (any, any, error) {
	if text, ok := value.(string); ok {
		return text, value, nil
	}
	reader, ok := value.(io.Reader)
	if !ok {
		return value, value, nil
	}
	upload, owned := value.(FileUpload)
	var closeOnce sync.Once
	var closeErr error
	closeSource := func() {
		if owned {
			closeOnce.Do(func() { closeErr = upload.Close() })
		}
	}
	cancelDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(cancelDone); closeSource() })
	data, readErr := io.ReadAll(multipartScalarReader{ctx, reader})
	if !stop() {
		<-cancelDone
	}
	closeSource()
	var replay any = bytes.NewReader(data)
	if owned {
		upload.Reader = bytes.NewReader(data)
		upload.Size, upload.KnownSize = int64(len(data)), true
		replay = upload
	}
	if err := ctx.Err(); err != nil {
		return nil, replay, err
	}
	if readErr != nil || closeErr != nil {
		// Reader errors may quote file contents. Keep only the known option name.
		return nil, replay, &MultipartScalarError{message: fmt.Sprintf("could not read --%s from its file", strings.ReplaceAll(name, "_", "-")), cause: errors.Join(readErr, closeErr)}
	}
	return string(data), replay, nil
}

type multipartScalarReader struct {
	context context.Context
	source  io.Reader
}

func (r multipartScalarReader) Read(buffer []byte) (int, error) {
	if err := r.context.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(buffer)
}
