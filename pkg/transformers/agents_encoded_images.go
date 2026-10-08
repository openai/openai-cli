package transformers

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

func summarizeAgentsUserImages(ctx context.Context, value gjson.Result) (readable.StreamEvent, bool, bool, error) {
	item := value.Get("item")
	if item.Get("role").Str != "user" {
		return readable.StreamEvent{}, false, false, nil
	}
	return summarizeAgentsInputImages(ctx, value, item.Get("content"))
}

func summarizeAgentsInputImages(ctx context.Context, value, content gjson.Result) (readable.StreamEvent, bool, bool, error) {
	if !content.IsArray() {
		return readable.StreamEvent{}, false, false, nil
	}
	var images []gjson.Result
	content.ForEach(func(_, part gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		if part.Get("type").Str == "input_image" {
			images = append(images, part.Get("image_url"))
		}
		return true
	})
	if err := ctx.Err(); err != nil {
		return readable.StreamEvent{}, false, false, err
	}
	summary, hidden, err := summarizeAgentsEncodedImages(ctx, value, images, "", "")
	if err != nil || !hidden {
		return readable.StreamEvent{}, false, false, err
	}
	// Keep the complete record and original part order, including legacy null IDs.
	return readable.StreamEvent{Details: summary}, true, true, nil
}

// Callers select only known image slots in source order. Replace their source
// spans once, preserving unfamiliar fields and avoiding one full copy per image.
func summarizeAgentsEncodedImages(ctx context.Context, value gjson.Result, images []gjson.Result, expectedMIME, label string) (gjson.Result, bool, error) {
	var out strings.Builder
	offset := 0
	for _, image := range images {
		if err := ctx.Err(); err != nil {
			return gjson.Result{}, false, err
		}
		if image.Type != gjson.String {
			continue
		}
		mediaType, encoded, ok := agentsImageDataURL(image.Str)
		if !ok || expectedMIME != "" && mediaType != expectedMIME {
			continue
		}
		characters, err := agentsImageBase64Length(ctx, encoded)
		if err != nil {
			return gjson.Result{}, false, err
		}
		if characters == 0 {
			continue
		}
		kind := label
		if kind == "" {
			kind = mediaType
			if kind == "" {
				kind = "image data"
			}
		}
		summary := fmt.Sprintf("(%s; %d base64 characters)", kind, characters)
		start := image.Index - value.Index
		out.WriteString(value.Raw[offset:start])
		out.WriteString(strconv.Quote(summary))
		offset = start + len(image.Raw)
	}
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, false, err
	}
	if offset == 0 {
		return value, false, nil
	}
	out.WriteString(value.Raw[offset:])
	return gjson.Parse(out.String()), true, nil
}

// Data URL parsing is local. Ordinary URLs and malformed values remain intact.
func agentsImageDataURL(value string) (mediaType, encoded string, ok bool) {
	if len(value) < 5 || !strings.EqualFold(value[:5], "data:") {
		return "", "", false
	}
	metadata, encoded, found := strings.Cut(value[5:], ",")
	const suffix = ";base64"
	if !found || len(metadata) < len(suffix) || !strings.EqualFold(metadata[len(metadata)-len(suffix):], suffix) {
		return "", "", false
	}
	metadata = metadata[:len(metadata)-len(suffix)]
	if metadata != "" {
		// A data URL can omit its media type while retaining parameters.
		omittedType := strings.HasPrefix(metadata, ";")
		if omittedType {
			metadata = "text/plain" + metadata
		}
		var err error
		mediaType, _, err = mime.ParseMediaType(metadata)
		if err != nil || !strings.Contains(mediaType, "/") {
			return "", "", false
		}
		if omittedType {
			mediaType = ""
		}
	}
	return mediaType, encoded, true
}

// Decode URL escapes while validating base64, without storing either complete
// decoded representation. Poll cancellation before each bounded source read.
func agentsImageBase64Length(ctx context.Context, encoded string) (int, error) {
	reader := &agentsImageDataReader{ctx: ctx, encoded: encoded}
	_, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding, reader))
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err != nil {
		return 0, nil
	}
	return reader.characters, nil
}

var errMalformedAgentsDataURL = errors.New("malformed image data URL")

type agentsImageDataReader struct {
	ctx                context.Context
	encoded            string
	offset, characters int
}

func (r *agentsImageDataReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.offset == len(r.encoded) {
		return 0, io.EOF
	}
	n := 0
	for n < min(len(p), 32*1024) && r.offset < len(r.encoded) {
		value := r.encoded[r.offset]
		if value == '%' {
			if r.offset+2 >= len(r.encoded) {
				return n, errMalformedAgentsDataURL
			}
			high, highOK := agentsURLHex(r.encoded[r.offset+1])
			low, lowOK := agentsURLHex(r.encoded[r.offset+2])
			if !highOK || !lowOK {
				return n, errMalformedAgentsDataURL
			}
			value = high<<4 | low
			r.offset += 3
		} else {
			r.offset++
		}
		p[n] = value
		n++
		r.characters++
	}
	return n, nil
}

func agentsURLHex(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}
