package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/openai/openai-cli/internal/apiform"
	"github.com/openai/openai-go/v3/option"
)

// multipartRequestBody streams multipart encoding through an io.Pipe. Encoding
// starts on the first Read so constructing request options never reads upload
// data or leaves an encoder goroutine blocked before the HTTP client takes
// ownership of the body.
type multipartRequestBody struct {
	reader          *io.PipeReader
	writer          *io.PipeWriter
	multipartWriter *multipart.Writer
	bodyMap         map[string]any
	encodingFormat  apiform.FormFormat
	contentType     string

	startOnce   sync.Once
	cleanupOnce sync.Once
	done        chan struct{}
	cleanupErr  error
}

var _ io.ReadCloser = (*multipartRequestBody)(nil)

func newMultipartRequestBody(bodyMap map[string]any, encodingFormat apiform.FormFormat) *multipartRequestBody {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	return makeMultipartRequestBody(reader, writer, multipartWriter, bodyMap, encodingFormat)
}

func newMultipartRequestBodyWithBoundary(
	bodyMap map[string]any,
	encodingFormat apiform.FormFormat,
	boundary string,
) (*multipartRequestBody, error) {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	if err := multipartWriter.SetBoundary(boundary); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, err
	}
	return makeMultipartRequestBody(reader, writer, multipartWriter, bodyMap, encodingFormat), nil
}

func makeMultipartRequestBody(
	reader *io.PipeReader,
	writer *io.PipeWriter,
	multipartWriter *multipart.Writer,
	bodyMap map[string]any,
	encodingFormat apiform.FormFormat,
) *multipartRequestBody {
	return &multipartRequestBody{
		reader:          reader,
		writer:          writer,
		multipartWriter: multipartWriter,
		bodyMap:         bodyMap,
		encodingFormat:  encodingFormat,
		contentType:     multipartWriter.FormDataContentType(),
		done:            make(chan struct{}),
	}
}

func (b *multipartRequestBody) Read(p []byte) (int, error) {
	b.start()
	return b.reader.Read(p)
}

func (b *multipartRequestBody) Close() error {
	// Closing the read side releases an encoder blocked on a pipe write. Closing
	// owned uploads also releases an encoder blocked while reading a local file.
	_ = b.reader.Close()
	cleanupErr := b.cleanup()

	// Starting after the read side is closed is safe and ensures the writer and
	// done channel are finalized even when the body was closed before its first
	// Read. The encoder's first pipe write fails immediately in that case.
	b.start()
	return cleanupErr
}

func (b *multipartRequestBody) ContentType() string {
	return b.contentType
}

func (b *multipartRequestBody) start() {
	b.startOnce.Do(func() {
		go b.encode()
	})
}

func (b *multipartRequestBody) encode() {
	defer close(b.done)

	if err := apiform.MarshalWithSettings(b.bodyMap, b.multipartWriter, b.encodingFormat); err != nil {
		// A final boundary asserts that every part completed. Abort immediately
		// instead of making a truncated source look like a complete upload.
		_ = b.writer.CloseWithError(errors.Join(err, b.cleanup()))
		return
	}

	writerCloseErr := b.multipartWriter.Close()
	cleanupErr := b.cleanup()
	_ = b.writer.CloseWithError(errors.Join(writerCloseErr, cleanupErr))
}

func (b *multipartRequestBody) cleanup() error {
	b.cleanupOnce.Do(func() {
		b.cleanupErr = closeFileUploads(b.bodyMap)
	})
	return b.cleanupErr
}

// multipartRequestOptions keeps scalar-only forms buffered and replayable.
// Forms containing an upload stream exactly once: the SDK retry loop is
// disabled because the CLI cannot promise that a reader will produce identical
// bytes a second time. Regular files retain an exact Content-Length; unknown
// sources use chunked transfer encoding.
func multipartRequestOptions(
	bodyMap map[string]any,
	encodingFormat apiform.FormFormat,
) ([]option.RequestOption, error) {
	info := inspectMultipartBody(bodyMap)
	if !info.hasUpload {
		return bufferedMultipartRequestOptions(bodyMap, encodingFormat)
	}

	boundary := multipart.NewWriter(io.Discard).Boundary()
	contentLength := int64(-1)
	if info.knownLength {
		var err error
		contentLength, err = multipartContentLength(bodyMap, encodingFormat, boundary)
		if err != nil {
			return nil, errors.Join(err, closeFileUploads(bodyMap))
		}
	}

	body, err := newMultipartRequestBodyWithBoundary(bodyMap, encodingFormat, boundary)
	if err != nil {
		return nil, errors.Join(err, closeFileUploads(bodyMap))
	}
	return []option.RequestOption{
		option.WithRequestBody(body.ContentType(), body),
		option.WithMaxRetries(0),
		option.WithMiddleware(streamedMultipartMiddleware(contentLength)),
	}, nil
}

func streamedMultipartMiddleware(contentLength int64) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if contentLength >= 0 {
			req.ContentLength = contentLength
		}

		// The SDK closes a request body after next returns, but a transport can
		// remain blocked in a streamed source read after the request is canceled.
		// Close the body independently to release owned files, and wait for an
		// already-started callback so it cannot outlive this middleware attempt.
		cancelDone := make(chan struct{})
		stopCancellation := context.AfterFunc(req.Context(), func() {
			defer close(cancelDone)
			if req.Body != nil {
				_ = req.Body.Close()
			}
		})
		res, err := next(req)
		if !stopCancellation() {
			<-cancelDone
		}
		if err != nil || res == nil ||
			(res.StatusCode != http.StatusTemporaryRedirect && res.StatusCode != http.StatusPermanentRedirect) {
			return res, err
		}

		location := res.Header.Get("Location")
		if res.Body != nil {
			_ = res.Body.Close()
		}
		return nil, fmt.Errorf(
			"cannot follow HTTP %d redirect to %q: streamed multipart uploads are not replayable",
			res.StatusCode,
			location,
		)
	}
}

func bufferedMultipartRequestOptions(
	bodyMap map[string]any,
	encodingFormat apiform.FormFormat,
) ([]option.RequestOption, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := apiform.MarshalWithSettings(bodyMap, writer, encodingFormat); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return []option.RequestOption{option.WithRequestBody(writer.FormDataContentType(), buf.Bytes())}, nil
}

type multipartBodyInfo struct {
	hasUpload   bool
	knownLength bool
}

func inspectMultipartBody(value any) multipartBodyInfo {
	switch value := value.(type) {
	case map[string]any:
		info := multipartBodyInfo{knownLength: true}
		for _, child := range value {
			childInfo := inspectMultipartBody(child)
			info.hasUpload = info.hasUpload || childInfo.hasUpload
			info.knownLength = info.knownLength && childInfo.knownLength
		}
		return info
	case []any:
		info := multipartBodyInfo{knownLength: true}
		for _, child := range value {
			childInfo := inspectMultipartBody(child)
			info.hasUpload = info.hasUpload || childInfo.hasUpload
			info.knownLength = info.knownLength && childInfo.knownLength
		}
		return info
	case fileUpload:
		return multipartBodyInfo{hasUpload: true, knownLength: value.hasKnownSize()}
	default:
		_, isReader := value.(io.Reader)
		return multipartBodyInfo{hasUpload: isReader, knownLength: !isReader}
	}
}

func multipartContentLength(
	bodyMap map[string]any,
	encodingFormat apiform.FormFormat,
	boundary string,
) (int64, error) {
	var uploadBytes int64
	emptyValue, err := transformFileUploads(bodyMap, func(upload fileUpload) (fileUpload, error) {
		if !upload.hasKnownSize() {
			return fileUpload{}, errors.New("cannot determine multipart length for an unknown-size upload")
		}
		if upload.size > math.MaxInt64-uploadBytes {
			return fileUpload{}, errors.New("multipart content length overflows int64")
		}
		uploadBytes += upload.size
		upload.Reader = strings.NewReader("")
		return upload, nil
	})
	if err != nil {
		return 0, err
	}

	var framing bytes.Buffer
	writer := multipart.NewWriter(&framing)
	if err := writer.SetBoundary(boundary); err != nil {
		return 0, err
	}
	if err := apiform.MarshalWithSettings(emptyValue, writer, encodingFormat); err != nil {
		return 0, err
	}
	if err := writer.Close(); err != nil {
		return 0, err
	}
	if int64(framing.Len()) > math.MaxInt64-uploadBytes {
		return 0, errors.New("multipart content length overflows int64")
	}
	return int64(framing.Len()) + uploadBytes, nil
}

func transformFileUploads(
	value any,
	transform func(fileUpload) (fileUpload, error),
) (any, error) {
	switch value := value.(type) {
	case fileUpload:
		return transform(value)
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			transformed, err := transformFileUploads(child, transform)
			if err != nil {
				return nil, err
			}
			result[key] = transformed
		}
		return result, nil
	case []any:
		result := make([]any, 0, len(value))
		for _, child := range value {
			transformed, err := transformFileUploads(child, transform)
			if err != nil {
				return nil, err
			}
			result = append(result, transformed)
		}
		return result, nil
	default:
		if _, isReader := value.(io.Reader); isReader {
			return nil, errors.New("multipart body contains an unknown-size reader")
		}
		return value, nil
	}
}

// closeFileUploads closes every source owned by a fileUpload, including values
// nested inside maps and arrays. Other io.ReadClosers (notably stdin wrappers)
// are intentionally left alone because this code does not own them.
func closeFileUploads(value any) error {
	return closeFileUploadsValue(reflect.ValueOf(value))
}

func closeFileUploadsValue(value reflect.Value) error {
	if !value.IsValid() {
		return nil
	}

	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}

	if value.CanInterface() {
		if upload, ok := value.Interface().(fileUpload); ok {
			return upload.Close()
		}
	}

	var errs []error
	switch value.Kind() {
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			errs = append(errs, closeFileUploadsValue(iter.Value()))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			errs = append(errs, closeFileUploadsValue(value.Index(i)))
		}
	}

	return errors.Join(errs...)
}
