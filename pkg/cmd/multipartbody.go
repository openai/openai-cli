package cmd

import (
	"bytes"
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

func (b *multipartRequestBody) Boundary() string {
	return b.multipartWriter.Boundary()
}

func (b *multipartRequestBody) start() {
	b.startOnce.Do(func() {
		go b.encode()
	})
}

func (b *multipartRequestBody) encode() {
	defer close(b.done)

	if err := apiform.MarshalWithSettings(b.bodyMap, b.multipartWriter, b.encodingFormat); err != nil {
		// A final multipart boundary asserts that every part completed. Abort the
		// pipe immediately on a source error instead of making a truncated file
		// look like a valid zero-byte upload to the receiver.
		_ = b.writer.CloseWithError(err)
		_ = b.cleanup()
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

// multipartRequestOptions preserves the SDK's buffered, replayable behavior
// when a multipart form contains only scalar fields. Uploads are streamed. A
// form containing only regular files is still replayable for HTTP redirects and
// carries an exact Content-Length; forms containing stdin, pipes, or arbitrary
// readers use chunked transfer and fail explicitly if a 307/308 requires replay.
func multipartRequestOptions(
	bodyMap map[string]any,
	encodingFormat apiform.FormFormat,
) ([]option.RequestOption, error) {
	info := inspectMultipartBody(bodyMap)
	if !info.hasUpload {
		return bufferedMultipartRequestOptions(bodyMap, encodingFormat)
	}

	boundary := multipart.NewWriter(io.Discard).Boundary()
	var contentLength int64
	if info.replayable {
		var err error
		contentLength, err = multipartContentLength(bodyMap, encodingFormat, boundary)
		if err != nil {
			return nil, err
		}
	}

	if info.replayable {
		return replayableMultipartRequestOptions(bodyMap, encodingFormat, boundary, contentLength)
	}

	body, err := newMultipartRequestBodyWithBoundary(bodyMap, encodingFormat, boundary)
	if err != nil {
		return nil, err
	}
	return []option.RequestOption{
		option.WithRequestBody(body.ContentType(), body),
		option.WithMaxRetries(0),
		option.WithMiddleware(rejectUnreplayableRedirect),
	}, nil
}

// openai-go v3.51 only installs GetBody for its concrete buffered body types.
// A streamed body therefore needs a replay marker on the SDK-owned request so
// its native retry loop runs. The middleware replaces that empty marker with a
// real multipart stream on every SDK attempt and supplies the same factory to
// net/http for 307/308 redirects. The CLI does not expose retry configuration,
// so the request fixes the SDK's current default of two retries; that bound also
// lets the middleware deterministically close its stable file descriptors.
const replayableMultipartMaxRetries = 2

type replayableMultipartRequest struct {
	bodyMap        map[string]any
	encodingFormat apiform.FormFormat
	boundary       string
	contentLength  int64

	mu       sync.Mutex
	attempts int
	closed   bool
	closeErr error
}

func replayableMultipartRequestOptions(
	bodyMap map[string]any,
	encodingFormat apiform.FormFormat,
	boundary string,
	contentLength int64,
) ([]option.RequestOption, error) {
	writer := multipart.NewWriter(io.Discard)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, err
	}
	request := &replayableMultipartRequest{
		bodyMap:        bodyMap,
		encodingFormat: encodingFormat,
		boundary:       boundary,
		contentLength:  contentLength,
	}

	// The empty *bytes.Reader is deliberate: it makes openai-go install GetBody
	// on its canonical request before retry decisions. middleware replaces the
	// marker on every attempt, so no marker bytes reach the network.
	return []option.RequestOption{
		option.WithRequestBody(writer.FormDataContentType(), bytes.NewReader(nil)),
		option.WithMaxRetries(replayableMultipartMaxRetries),
		option.WithMiddleware(request.middleware),
	}, nil
}

func (r *replayableMultipartRequest) middleware(
	req *http.Request,
	next option.MiddlewareNext,
) (*http.Response, error) {
	attemptIndex := r.beginAttempt()
	attempt := &multipartReplayAttempt{request: r}

	// Discard the SDK's empty replay marker before installing the actual body.
	if req.Body != nil {
		_ = req.Body.Close()
	}
	body, err := attempt.newBody()
	if err != nil {
		return nil, errors.Join(err, r.close())
	}
	req.Body = body
	req.ContentLength = r.contentLength
	req.GetBody = attempt.newBody

	res, requestErr := next(req)
	attemptErr := attempt.close()
	if req.Context().Err() != nil ||
		!shouldRetryReplayableMultipart(res) ||
		attemptIndex >= replayableMultipartMaxRetries {
		return res, errors.Join(requestErr, attemptErr, r.close())
	}
	return res, errors.Join(requestErr, attemptErr)
}

func (r *replayableMultipartRequest) beginAttempt() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	attempt := r.attempts
	r.attempts++
	return attempt
}

func (r *replayableMultipartRequest) newBody() (*multipartRequestBody, error) {
	bodyMap, err := replayMultipartBody(r.bodyMap)
	if err != nil {
		return nil, err
	}
	return newMultipartRequestBodyWithBoundary(bodyMap, r.encodingFormat, r.boundary)
}

func (r *replayableMultipartRequest) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		r.closeErr = closeFileUploads(r.bodyMap)
	}
	return r.closeErr
}

type multipartReplayAttempt struct {
	request *replayableMultipartRequest

	mu     sync.Mutex
	bodies []*multipartRequestBody
	closed bool
}

func (a *multipartReplayAttempt) newBody() (io.ReadCloser, error) {
	body, err := a.request.newBody()
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		_ = body.Close()
		return nil, errors.New("multipart replay attempt is closed")
	}
	a.bodies = append(a.bodies, body)
	return body, nil
}

func (a *multipartReplayAttempt) close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	bodies := append([]*multipartRequestBody(nil), a.bodies...)
	a.mu.Unlock()

	var errs []error
	for _, body := range bodies {
		errs = append(errs, body.Close())
		<-body.done
	}
	return errors.Join(errs...)
}

// Keep this predicate aligned with openai-go's retry policy. It is only used
// to decide when the request-level file descriptors can be closed; the SDK
// still owns retry timing, response cleanup, and the retry decision itself.
func shouldRetryReplayableMultipart(res *http.Response) bool {
	if res == nil {
		return true
	}
	if res.Header.Get("x-should-retry") == "true" {
		return true
	}
	if res.Header.Get("x-should-retry") == "false" {
		return false
	}
	return res.StatusCode == http.StatusRequestTimeout ||
		res.StatusCode == http.StatusConflict ||
		res.StatusCode == http.StatusTooManyRequests ||
		res.StatusCode >= http.StatusInternalServerError
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

func rejectUnreplayableRedirect(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	res, err := next(req)
	if err != nil || res == nil || (res.StatusCode != http.StatusTemporaryRedirect && res.StatusCode != http.StatusPermanentRedirect) {
		return res, err
	}

	location := res.Header.Get("Location")
	if res.Body != nil {
		_ = res.Body.Close()
	}
	return res, fmt.Errorf(
		"cannot follow HTTP %d redirect to %q: multipart upload contains a non-replayable source",
		res.StatusCode,
		location,
	)
}

type multipartBodyInfo struct {
	hasUpload  bool
	replayable bool
}

func inspectMultipartBody(value any) multipartBodyInfo {
	switch value := value.(type) {
	case map[string]any:
		info := multipartBodyInfo{replayable: true}
		for _, child := range value {
			childInfo := inspectMultipartBody(child)
			info.hasUpload = info.hasUpload || childInfo.hasUpload
			info.replayable = info.replayable && childInfo.replayable
		}
		return info
	case []any:
		info := multipartBodyInfo{replayable: true}
		for _, child := range value {
			childInfo := inspectMultipartBody(child)
			info.hasUpload = info.hasUpload || childInfo.hasUpload
			info.replayable = info.replayable && childInfo.replayable
		}
		return info
	case fileUpload:
		return multipartBodyInfo{hasUpload: true, replayable: value.canReplay()}
	default:
		_, isReader := value.(io.Reader)
		return multipartBodyInfo{hasUpload: isReader, replayable: !isReader}
	}
}

func multipartContentLength(
	bodyMap map[string]any,
	encodingFormat apiform.FormFormat,
	boundary string,
) (int64, error) {
	var uploadBytes int64
	emptyValue, err := transformFileUploads(bodyMap, func(upload fileUpload) (fileUpload, error) {
		if !upload.canReplay() {
			return fileUpload{}, errors.New("cannot determine multipart length for a non-replayable upload")
		}
		if upload.size > math.MaxInt64-uploadBytes {
			return fileUpload{}, errors.New("multipart content length overflows int64")
		}
		uploadBytes += upload.size
		upload.Reader = strings.NewReader("")
		upload.ownsSource = false
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

func replayMultipartBody(bodyMap map[string]any) (map[string]any, error) {
	value, err := transformFileUploads(bodyMap, func(upload fileUpload) (fileUpload, error) {
		return upload.replay()
	})
	if err != nil {
		return nil, err
	}
	reopened, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("multipart body must be a map")
	}
	return reopened, nil
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
				return nil, errors.Join(err, closeFileUploads(result))
			}
			result[key] = transformed
		}
		return result, nil
	case []any:
		result := make([]any, 0, len(value))
		for _, child := range value {
			transformed, err := transformFileUploads(child, transform)
			if err != nil {
				return nil, errors.Join(err, closeFileUploads(result))
			}
			result = append(result, transformed)
		}
		return result, nil
	default:
		if _, isReader := value.(io.Reader); isReader {
			return nil, errors.New("multipart body contains a non-replayable reader")
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
