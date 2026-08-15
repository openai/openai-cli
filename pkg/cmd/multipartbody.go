package cmd

import (
	"errors"
	"io"
	"mime/multipart"
	"reflect"
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

	encodeErr := apiform.MarshalWithSettings(b.bodyMap, b.multipartWriter, b.encodingFormat)
	writerCloseErr := b.multipartWriter.Close()
	cleanupErr := b.cleanup()

	_ = b.writer.CloseWithError(errors.Join(encodeErr, writerCloseErr, cleanupErr))
}

func (b *multipartRequestBody) cleanup() error {
	b.cleanupOnce.Do(func() {
		b.cleanupErr = closeFileUploads(b.bodyMap)
	})
	return b.cleanupErr
}

func multipartRequestOptions(body *multipartRequestBody) []option.RequestOption {
	return []option.RequestOption{
		option.WithRequestBody(body.ContentType(), body),
		// A streamed multipart body cannot be replayed. Make the behavior
		// explicit even though the SDK also avoids retrying bodies without
		// an http.Request.GetBody function.
		option.WithMaxRetries(0),
	}
}

// closeFileUploads closes every file opened by openFileUpload, including files
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
