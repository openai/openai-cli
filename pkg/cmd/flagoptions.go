package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/apiform"
	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/debugmiddleware"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3/option"

	"github.com/goccy/go-yaml"
	"github.com/urfave/cli/v3"
)

type BodyContentType int

const (
	EmptyBody BodyContentType = iota
	MultipartFormEncoded
	ApplicationJSON
	ApplicationOctetStream
)

type FileEmbedStyle int

const (
	// EmbedText reads referenced files fully into memory and substitutes the file's contents back into the
	// value as a string. Binary files are base64-encoded. Used for JSON request bodies and for headers and
	// query parameters, where the file contents need to be serialized inline.
	EmbedText FileEmbedStyle = iota

	// EmbedIOReader replaces file references with an io.Reader that streams the file's contents. Used for
	// `multipart/form-data` and `application/octet-stream` request bodies, where files are uploaded as binary
	// parts rather than embedded into a text value.
	EmbedIOReader
)

// onceStdinReader wraps an io.Reader that can only be consumed once, used to ensure stdin is read by at most
// one parameter (or only for a body root parameter or only for YAML parameter input). If reason is set, stdin
// is unavailable and read() returns an error explaining why.
type onceStdinReader struct {
	stdinReader   io.Reader
	failureReason string
}

func (o *onceStdinReader) read() (io.Reader, error) {
	if o.failureReason != "" {
		return nil, fmt.Errorf("cannot read from stdin: %s", o.failureReason)
	}
	if o.stdinReader == nil {
		return nil, fmt.Errorf("stdin has already been read by another parameter; it can only be read once")
	}
	r := o.stdinReader
	o.stdinReader = nil
	return r, nil
}

func (o *onceStdinReader) readAll() ([]byte, error) {
	r, err := o.read()
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func isStdinPath(s string) bool {
	switch s {
	case "-", "/dev/fd/0", "/dev/stdin":
		return true
	}
	return false
}

func embedFiles(obj any, embedStyle FileEmbedStyle, stdin *onceStdinReader) (any, error) {
	if obj == nil {
		return obj, nil
	}
	v := reflect.ValueOf(obj)
	result, err := embedFilesValue(v, embedStyle, stdin)
	if err != nil {
		return nil, err
	}
	return result.Interface(), nil
}

// Replace "@file.txt" with the file's contents inside a value
func embedFilesValue(v reflect.Value, embedStyle FileEmbedStyle, stdin *onceStdinReader) (reflect.Value, error) {
	// Unwrap interface values to get the concrete type
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return v, nil
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Map:
		if v.Len() == 0 {
			return v, nil
		}
		// Always create map[string]any to handle potential type changes when embedding files
		result := reflect.MakeMap(reflect.TypeOf(map[string]any{}))

		iter := v.MapRange()
		for iter.Next() {
			key := iter.Key()
			val := iter.Value()
			newVal, err := embedFilesValue(val, embedStyle, stdin)
			if err != nil {
				return reflect.Value{}, err
			}
			result.SetMapIndex(key, newVal)
		}
		return result, nil

	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return v, nil
		}
		// Use `[]any` to allow for types to change when embedding files
		result := reflect.MakeSlice(reflect.TypeOf([]any{}), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			newVal, err := embedFilesValue(v.Index(i), embedStyle, stdin)
			if err != nil {
				return reflect.Value{}, err
			}
			result.Index(i).Set(newVal)
		}
		return result, nil

	case reflect.String:
		// FilePathValue is always treated as a file path without needing the "@" prefix.
		// These only appear on binary upload parameters (multipart/octet-stream), which
		// always use EmbedIOReader.
		if v.Type() == reflect.TypeOf(FilePathValue("")) {
			s := v.String()
			if s == "" {
				return v, nil
			}
			if embedStyle == EmbedIOReader {
				if isStdinPath(s) {
					r, err := stdin.read()
					if err != nil {
						return v, err
					}
					return reflect.ValueOf(io.NopCloser(r)), nil
				}
				upload, err := openFileUpload(s)
				if err != nil {
					return v, err
				}
				return reflect.ValueOf(upload), nil
			}
			if isStdinPath(s) {
				content, err := stdin.readAll()
				if err != nil {
					return v, err
				}
				return reflect.ValueOf(string(content)), nil
			}
			content, err := os.ReadFile(s)
			if err != nil {
				return v, err
			}
			return reflect.ValueOf(string(content)), nil
		}

		s := v.String()
		if literal, ok := strings.CutPrefix(s, "\\@"); ok {
			// Allow for escaped @ signs if you don't want them to be treated as files
			return reflect.ValueOf("@" + literal), nil
		}

		if embedStyle == EmbedText {
			if filename, ok := strings.CutPrefix(s, "@data://"); ok {
				// The "@data://" prefix is for files you explicitly want to upload
				// as base64-encoded (even if the file itself is plain text)
				if isStdinPath(filename) {
					content, err := stdin.readAll()
					if err != nil {
						return v, err
					}
					return reflect.ValueOf(base64.StdEncoding.EncodeToString(content)), nil
				}
				content, err := os.ReadFile(filename)
				if err != nil {
					return v, err
				}
				return reflect.ValueOf(base64.StdEncoding.EncodeToString(content)), nil
			} else if filename, ok := strings.CutPrefix(s, "@file://"); ok {
				// The "@file://" prefix is for files that you explicitly want to
				// upload as a string literal with backslash escapes (not base64
				// encoded)
				if isStdinPath(filename) {
					content, err := stdin.readAll()
					if err != nil {
						return v, err
					}
					return reflect.ValueOf(string(content)), nil
				}
				content, err := os.ReadFile(filename)
				if err != nil {
					return v, err
				}
				return reflect.ValueOf(string(content)), nil
			} else if filename, ok := strings.CutPrefix(s, "@"); ok {
				if isStdinPath(filename) {
					content, err := stdin.readAll()
					if err != nil {
						return v, err
					}
					if isUTF8TextFile(content) {
						return reflect.ValueOf(string(content)), nil
					}
					return reflect.ValueOf(base64.StdEncoding.EncodeToString(content)), nil
				}
				content, err := os.ReadFile(filename)
				if err != nil {
					// If the string is "@username", it's probably supposed to be a
					// string literal and not a file reference. However, if the
					// string looks like "@file.txt" or "@/tmp/file", then it's
					// probably supposed to be a file.
					probablyFile := strings.Contains(filename, ".") || strings.Contains(filename, "/")
					if probablyFile {
						// Give a useful error message if the user tried to upload a
						// file, but the file couldn't be read (e.g. mistyped
						// filename or permission error)
						return v, err
					}
					// Fall back to the raw value if the user provided something
					// like "@username" that's not intended to be a file.
					return v, nil
				}
				// If the file looks like a plain text UTF8 file format, then use the contents directly.
				if isUTF8TextFile(content) {
					return reflect.ValueOf(string(content)), nil
				}
				// Otherwise it's a binary file, so encode it with base64
				return reflect.ValueOf(base64.StdEncoding.EncodeToString(content)), nil
			}
		} else {
			if filename, ok := strings.CutPrefix(s, "@"); ok {
				// Behavior is the same for @file, @data://file, and @file://file, except that
				// @username will be treated as a literal string if no "username" file exists
				expectsFile := true
				if withoutPrefix, ok := strings.CutPrefix(filename, "data://"); ok {
					filename = withoutPrefix
				} else if withoutPrefix, ok := strings.CutPrefix(filename, "file://"); ok {
					filename = withoutPrefix
				} else {
					expectsFile = strings.Contains(filename, ".") || strings.Contains(filename, "/")
				}

				if isStdinPath(filename) {
					r, err := stdin.read()
					if err != nil {
						return v, err
					}
					return reflect.ValueOf(io.NopCloser(r)), nil
				}

				upload, err := openFileUpload(filename)
				if err != nil {
					if !expectsFile {
						// For strings that start with "@" and don't look like a filename, return the string
						return v, nil
					}
					return v, err
				}
				return reflect.ValueOf(upload), nil
			}
		}
		return v, nil

	default:
		return v, nil
	}
}

// Guess whether a file's contents are binary (e.g. a .jpg or .mp3), as opposed
// to plain text (e.g. .txt or .md).
func isUTF8TextFile(content []byte) bool {
	// Go's DetectContentType follows https://mimesniff.spec.whatwg.org/ and
	// these are the sniffable content types that are plain text:
	textTypes := []string{
		"text/",
		"application/json",
		"application/xml",
		"application/javascript",
		"application/x-javascript",
		"application/ecmascript",
		"application/x-ecmascript",
	}

	contentType := http.DetectContentType(content)
	for _, prefix := range textTypes {
		if strings.HasPrefix(contentType, prefix) {
			return utf8.Valid(content)
		}
	}
	return false
}

func flagOptions(
	cmd *cli.Command,
	nestedFormat apiquery.NestedQueryFormat,
	arrayFormat apiquery.ArrayQueryFormat,
	bodyType BodyContentType,

	// This parameter is true if stdin is already in use to pass a binary parameter by using the special value
	// "-". In this case, we won't attempt to read it as a JSON/YAML blob for options setting.
	ignoreStdin bool,
) ([]option.RequestOption, error) {
	var options []option.RequestOption
	if cmd.Bool("debug") {
		options = append(options, option.WithMiddleware(debugmiddleware.NewRequestLogger().Middleware()))
	}

	requestContents := requestflag.ExtractRequestContents(cmd)

	// Translate inner-field aliases in YAML values that came from flags (e.g.
	// `--parent '{"alias": val}'` resolving to the canonical inner field).
	if bodyMap, ok := requestContents.Body.(map[string]any); ok {
		applyDataAliases(cmd, bodyMap)
	}

	stdinConsumedByPipe := false
	if bodyType != ApplicationOctetStream && !ignoreStdin && isInputPiped() {
		pipeData, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, err
		}

		if len(pipeData) > 0 {
			stdinConsumedByPipe = true
			var bodyData any
			if err := yaml.Unmarshal(pipeData, &bodyData); err != nil {
				return nil, fmt.Errorf("Failed to parse piped data as YAML/JSON:\n%w", err)
			}
			if bodyMap, ok := bodyData.(map[string]any); ok {
				applyDataAliases(cmd, bodyMap)
				// Apply any matching keys from the piped data to path, query, and header flags
				// that have not already been set via the command line.
				if err := requestflag.ApplyStdinDataToFlags(cmd, bodyMap); err != nil {
					return nil, err
				}
				// Re-extract request contents now that flags may have been updated.
				requestContents = requestflag.ExtractRequestContents(cmd)
				// Remove keys that were consumed as query, header, or path params so they
				// don't also leak into the request body via the maps.Copy merge below.
				// We delete both the canonical key and any aliases since the user may have
				// piped data using an alias name rather than the canonical API name.
				for _, flag := range cmd.Flags {
					inReq, ok := flag.(requestflag.InRequest)
					if !ok || !flag.IsSet() {
						continue
					}
					if inReq.GetQueryPath() != "" || inReq.GetHeaderPath() != "" || inReq.GetPathParam() != "" {
						delete(bodyMap, inReq.GetQueryPath())
						delete(bodyMap, inReq.GetHeaderPath())
						delete(bodyMap, inReq.GetPathParam())
						for _, alias := range inReq.GetDataAliases() {
							delete(bodyMap, alias)
						}
					}
				}
				if bodyType != EmptyBody {
					if flagMap, ok := requestContents.Body.(map[string]any); ok {
						maps.Copy(bodyMap, flagMap)
						requestContents.Body = bodyMap
					} else {
						bodyData = requestContents.Body
					}
				}
			} else if bodyType != EmptyBody {
				if flagMap, ok := requestContents.Body.(map[string]any); ok && len(flagMap) > 0 {
					return nil, fmt.Errorf("Cannot merge flags with a body that is not a map: %v", bodyData)
				} else {
					requestContents.Body = bodyData
				}
			}
		}
	}

	if missingFlags := requestflag.GetMissingRequiredFlags(cmd, requestContents.Body); len(missingFlags) > 0 {
		if len(missingFlags) == 1 {
			return nil, fmt.Errorf("Required flag %q not set\nRun '%s --help' for usage information", missingFlags[0].Names()[0], cmd.FullName())
		} else {
			names := []string{}
			for _, flag := range missingFlags {
				names = append(names, flag.Names()[0])
			}
			return nil, fmt.Errorf("Required flags %q not set\nRun '%s --help' for usage information", strings.Join(names, ", "), cmd.FullName())
		}
	}

	// For flags marked as FileInput (type: string, format: binary), the value is always
	// a file path. Wrap with FilePathValue so embedFiles reads the file automatically
	// without requiring the user to type the "@" prefix. This handles both values set
	// via explicit CLI flags and values that arrived via piped YAML/JSON data.
	wrapFileInputValues(cmd, &requestContents)

	// Determine stdin availability for FileInput params that use "-".
	var stdinReader onceStdinReader
	if ignoreStdin {
		stdinReader = onceStdinReader{failureReason: "stdin is already being used for the request body"}
	} else if stdinConsumedByPipe {
		stdinReader = onceStdinReader{failureReason: "stdin was already consumed by piped YAML/JSON input"}
	} else {
		stdinReader = onceStdinReader{stdinReader: os.Stdin}
	}

	// Embed files passed as "@file.jpg" in the request body, headers, and query:
	embedStyle := EmbedText
	if bodyType == ApplicationOctetStream || bodyType == MultipartFormEncoded {
		embedStyle = EmbedIOReader
	}

	if embedded, err := embedFiles(requestContents.Body, embedStyle, &stdinReader); err != nil {
		return nil, err
	} else {
		requestContents.Body = embedded
	}

	if headersWithFiles, err := embedFiles(requestContents.Headers, EmbedText, &stdinReader); err != nil {
		return nil, err
	} else {
		requestContents.Headers = headersWithFiles.(map[string]any)
	}
	if queriesWithFiles, err := embedFiles(requestContents.Queries, EmbedText, &stdinReader); err != nil {
		return nil, err
	} else {
		requestContents.Queries = queriesWithFiles.(map[string]any)
	}

	querySettings := apiquery.QuerySettings{
		NestedFormat: nestedFormat,
		ArrayFormat:  arrayFormat,
	}

	// Add query parameters:
	if values, err := apiquery.MarshalWithSettings(requestContents.Queries, querySettings); err != nil {
		return nil, err
	} else {
		for k, vs := range values {
			if len(vs) == 0 {
				options = append(options, option.WithQueryDel(k))
			} else {
				options = append(options, option.WithQuery(k, vs[0]))
				for _, v := range vs[1:] {
					options = append(options, option.WithQueryAdd(k, v))
				}
			}
		}
	}

	// Add header parameters
	headerSettings := apiquery.QuerySettings{
		NestedFormat: apiquery.NestedQueryFormatDots,
		ArrayFormat:  apiquery.ArrayQueryFormatRepeat,
	}
	if values, err := apiquery.MarshalWithSettings(requestContents.Headers, headerSettings); err != nil {
		return nil, err
	} else {
		for k, vs := range values {
			if len(vs) == 0 {
				options = append(options, option.WithHeaderDel(k))
			} else {
				options = append(options, option.WithHeader(k, vs[0]))
				for _, v := range vs[1:] {
					options = append(options, option.WithHeaderAdd(k, v))
				}
			}
		}
	}

	switch bodyType {
	case EmptyBody:
		break
	case MultipartFormEncoded:
		// For multipart/form-encoded, we need a map structure
		bodyMap, ok := requestContents.Body.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Cannot send a non-map value to a form-encoded endpoint: %v\n", requestContents.Body)
		}
		encodingFormat := apiform.FormatBrackets
		contentType, body := newMultipartRequestBody(bodyMap, encodingFormat)
		options = append(options,
			option.WithRequestBody(contentType, body),
			// Streaming request bodies cannot be replayed safely by the SDK retry loop.
			option.WithMaxRetries(0),
		)

	case ApplicationJSON:
		bodyBytes, err := json.Marshal(requestContents.Body)
		if err != nil {
			return nil, err
		}
		options = append(options, option.WithRequestBody("application/json", bodyBytes))

	case ApplicationOctetStream:
		// If there is a body root parameter, that will handle setting the request body, we don't need to do it here.
		for _, flag := range cmd.Flags {
			if toSend, ok := flag.(requestflag.InRequest); ok && toSend.IsBodyRoot() {
				return options, nil
			}
		}
		if bodyBytes, ok := requestContents.Body.([]byte); ok {
			options = append(options, option.WithRequestBody("application/octet-stream", bodyBytes))
		} else if bodyStr, ok := requestContents.Body.(string); ok {
			options = append(options, option.WithRequestBody("application/octet-stream", []byte(bodyStr)))
		} else {
			return nil, fmt.Errorf("Unsupported body for application/octet-stream: %v", requestContents.Body)
		}

	default:
		panic("Invalid body content type!")
	}

	return options, nil
}

func newMultipartRequestBody(bodyMap map[string]any, encodingFormat apiform.FormFormat) (string, io.Reader) {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	contentType := multipartWriter.FormDataContentType()

	go func() {
		err := apiform.MarshalWithSettings(bodyMap, multipartWriter, encodingFormat)
		if closeErr := multipartWriter.Close(); err == nil {
			err = closeErr
		}
		if closeErr := closeFileUploads(bodyMap); err == nil {
			err = closeErr
		}

		if err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		_ = writer.Close()
	}()

	return contentType, reader
}

func closeFileUploads(value any) error {
	return closeFileUploadsValue(reflect.ValueOf(value))
}

func closeFileUploadsValue(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}

	if v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return closeFileUploadsValue(v.Elem())
	}

	if upload, ok := v.Interface().(fileUpload); ok {
		return upload.Close()
	}

	switch v.Kind() {
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			if err := closeFileUploadsValue(iter.Value()); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := closeFileUploadsValue(v.Index(i)); err != nil {
				return err
			}
		}
	}

	return nil
}

// FilePathValue is a string wrapper that marks a value as a file path whose contents should be read
// and embedded in the request. Unlike a regular string, embedFilesValue always treats a FilePathValue
// as a file path without needing the "@" prefix.
type FilePathValue string

// fileUpload wraps an io.Reader with filename and content-type metadata for
// use as a multipart form part. The apiform encoder detects the Filename and
// ContentType methods and uses them to populate the Content-Disposition
// filename and the Content-Type header on the part.
type fileUpload struct {
	io.Reader   // apiform checks for reader and reads its contents during encode
	filename    string
	contentType string
}

func (f fileUpload) Filename() string    { return f.filename }
func (f fileUpload) ContentType() string { return f.contentType }
func (f fileUpload) Close() error {
	if c, ok := f.Reader.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// openFileUpload opens the file at path and returns a fileUpload whose filename
// is the path's basename and whose content type is derived from the file
// extension (falling back to application/octet-stream when unknown).
func openFileUpload(path string) (fileUpload, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileUpload{}, err
	}
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return fileUpload{
		Reader:      file,
		filename:    filepath.Base(path),
		contentType: contentType,
	}, nil
}

// applyDataAliases rewrites keys in a body map based on flag `DataAliases` metadata. For top-level flags,
// `{alias: value}` becomes `{canonical: value}`. For inner flags (those registered under an outer flag
// via WithInnerFlags), the alias translation is also applied to the nested map under the outer flag's
// body path, so values like `--parent '{"alias": val}'` resolve to the canonical inner field name.
func applyDataAliases(cmd *cli.Command, bodyMap map[string]any) {
	for _, flag := range cmd.Flags {
		// Inner flags: rewrite aliases inside the nested map under the outer flag's body path.
		if inner, ok := flag.(requestflag.HasOuterFlag); ok {
			outer, outerOk := inner.GetOuterFlag().(requestflag.InRequest)
			if !outerOk {
				continue
			}
			if nested, ok := bodyMap[outer.GetBodyPath()].(map[string]any); ok && inner.GetInnerField() != "" {
				rewriteAliases(nested, inner.GetInnerField(), inner.GetDataAliases())
			}
			continue
		}
		// Top-level flags: rewrite aliases in the body map.
		if inReq, ok := flag.(requestflag.InRequest); ok && inReq.GetBodyPath() != "" {
			rewriteAliases(bodyMap, inReq.GetBodyPath(), inReq.GetDataAliases())
		}
	}
}

// rewriteAliases replaces each alias key in m with the canonical key, preserving the value. The
// "canonical" key is the name the API itself expects (the OpenAPI property/field name) — e.g. for
// a top-level flag, the parameter's BodyPath; for an inner flag, the inner field name. Aliases are
// the user-facing alternate names declared via x-stainless-cli-data-alias.
func rewriteAliases(m map[string]any, canonical string, aliases []string) {
	for _, alias := range aliases {
		if alias == "" || alias == canonical {
			continue
		}
		if val, exists := m[alias]; exists {
			m[canonical] = val
			delete(m, alias)
		}
	}
}

// wrapFileInputValues replaces string values for FileInput flags (type: string, format: binary) with
// FilePathValue sentinel values. embedFilesValue recognizes FilePathValue and reads the file contents
// directly, so the user doesn't need to type the "@" prefix. This handles both values set via explicit
// CLI flags and values that arrived via piped YAML/JSON data.
func wrapFileInputValues(cmd *cli.Command, contents *requestflag.RequestContents) {
	bodyMap, _ := contents.Body.(map[string]any)

	for _, flag := range cmd.Flags {
		inReq, ok := flag.(requestflag.InRequest)
		if !ok || !inReq.IsFileInput() || inReq.IsBodyRoot() {
			continue
		}

		// Wrap values set via explicit CLI flags.
		if flag.IsSet() {
			if wrapped, changed := wrapFileInputValue(flag.Get()); changed {
				if bodyPath := inReq.GetBodyPath(); bodyPath != "" {
					if bodyMap != nil {
						bodyMap[bodyPath] = wrapped
					}
				} else if queryPath := inReq.GetQueryPath(); queryPath != "" {
					contents.Queries[queryPath] = wrapped
				} else if headerPath := inReq.GetHeaderPath(); headerPath != "" {
					contents.Headers[headerPath] = wrapped
				}
			}
		}

		// Wrap values that arrived via piped YAML/JSON data in the body map.
		if bodyPath := inReq.GetBodyPath(); bodyPath != "" && bodyMap != nil {
			if value, exists := bodyMap[bodyPath]; exists {
				if wrapped, changed := wrapFileInputValue(value); changed {
					bodyMap[bodyPath] = wrapped
				}
			}
		}
	}
}

func wrapFileInputValue(value any) (any, bool) {
	switch v := value.(type) {
	case string:
		if v == "" {
			return value, false
		}
		return FilePathValue(v), true

	case []string:
		result := make([]any, len(v))
		for i, s := range v {
			result[i] = FilePathValue(s)
		}
		return result, true

	case []any:
		result := make([]any, len(v))
		for i, elem := range v {
			if s, ok := elem.(string); ok {
				result[i] = FilePathValue(s)
			} else {
				result[i] = elem
			}
		}
		return result, true

	default:
		return value, false
	}
}
