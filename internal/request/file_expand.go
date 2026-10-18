package request

import (
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"unicode/utf8"
)

// FileEmbedStyle selects text substitution or streaming upload expansion.
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

// EmbedFiles expands trusted file references and preserves borrowed stdin ownership.
func EmbedFiles(obj any, embedStyle FileEmbedStyle, stdin *OnceStdinReader) (any, error) {
	return embedFilesForOS(obj, embedStyle, stdin, runtime.GOOS)
}

func embedFilesForOS(obj any, embedStyle FileEmbedStyle, stdin *OnceStdinReader, goos string, sources ...FileInputSource) (any, error) {
	if obj == nil {
		return obj, nil
	}
	v := reflect.ValueOf(obj)
	result, err := embedFilesValue(v, embedStyle, stdin, goos, sources...)
	if err != nil {
		return nil, err
	}
	return result.Interface(), nil
}

// Replace "@file.txt" with the file's contents inside a value
func embedFilesValue(v reflect.Value, embedStyle FileEmbedStyle, stdin *OnceStdinReader, goos string, sources ...FileInputSource) (reflect.Value, error) {
	var source FileInputSource
	if len(sources) != 0 {
		source = sources[0]
	}
	// Unwrap interface values to get the concrete type
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return v, nil
		}
		v = v.Elem()
	}
	// Nullable string flags use pointers. Expand their values just like ordinary
	// strings, retaining nil as JSON null and leaving other pointer types alone.
	if v.Type() == reflect.TypeFor[*string]() && !v.IsNil() {
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
			newVal, err := embedFilesValue(val, embedStyle, stdin, goos, source.child(key.String()))
			if err != nil {
				return reflect.Value{}, errors.Join(err, CloseFileUploads(result.Interface()))
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
			newVal, err := embedFilesValue(v.Index(i), embedStyle, stdin, goos, source)
			if err != nil {
				return reflect.Value{}, errors.Join(err, CloseFileUploads(result.Slice(0, i).Interface()))
			}
			result.Index(i).Set(newVal)
		}
		return result, nil

	case reflect.String:
		if v.Type() == reflect.TypeOf(UntrustedStdinValue("")) {
			return reflect.ValueOf(v.String()), nil
		}

		// FilePathValue is always treated as a file path without needing the "@" prefix.
		// These only appear on binary upload parameters (multipart/octet-stream), which
		// always use EmbedIOReader.
		if v.Type() == reflect.TypeOf(FilePathValue("")) {
			s := v.String()
			if s == "" {
				return v, nil
			}
			if embedStyle == EmbedIOReader {
				if IsStdinPath(s) {
					r, err := stdin.Take()
					if err != nil {
						return v, err
					}
					return reflect.ValueOf(io.NopCloser(r)), nil
				}
				upload, err := source.OpenUpload(s)
				if err != nil {
					return v, err
				}
				return reflect.ValueOf(upload), nil
			}
			if IsStdinPath(s) {
				content, err := stdin.ReadAll()
				if err != nil {
					return v, err
				}
				return reflect.ValueOf(string(content)), nil
			}
			content, err := source.ReadFile(s)
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
				if IsStdinPath(filename) {
					content, err := stdin.ReadAll()
					if err != nil {
						return v, err
					}
					return reflect.ValueOf(base64.StdEncoding.EncodeToString(content)), nil
				}
				content, err := source.ReadFile(filename)
				if err != nil {
					return v, err
				}
				return reflect.ValueOf(base64.StdEncoding.EncodeToString(content)), nil
			} else if filename, ok := strings.CutPrefix(s, "@file://"); ok {
				// The "@file://" prefix is for files that you explicitly want to
				// upload as a string literal with backslash escapes (not base64
				// encoded)
				if IsStdinPath(filename) {
					content, err := stdin.ReadAll()
					if err != nil {
						return v, err
					}
					return reflect.ValueOf(string(content)), nil
				}
				content, err := source.ReadFile(filename)
				if err != nil {
					return v, err
				}
				return reflect.ValueOf(string(content)), nil
			} else if filename, ok := strings.CutPrefix(s, "@"); ok {
				if IsStdinPath(filename) {
					content, err := stdin.ReadAll()
					if err != nil {
						return v, err
					}
					if IsUTF8TextFile(content) {
						return reflect.ValueOf(string(content)), nil
					}
					return reflect.ValueOf(base64.StdEncoding.EncodeToString(content)), nil
				}
				content, err := source.ReadFile(filename)
				if err != nil {
					// If the string is "@username", it's probably supposed to be a
					// string literal and not a file reference. However, if the
					// string looks like "@file.txt" or "@/tmp/file", then it's
					// probably supposed to be a file.
					probablyFile := looksLikeFilePath(filename, goos)
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
				if IsUTF8TextFile(content) {
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
					expectsFile = looksLikeFilePath(filename, goos)
				}

				if IsStdinPath(filename) {
					r, err := stdin.Take()
					if err != nil {
						return v, err
					}
					return reflect.ValueOf(io.NopCloser(r)), nil
				}

				upload, err := source.OpenUpload(filename)
				if err != nil {
					if !expectsFile && errors.Is(err, fs.ErrNotExist) {
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

// Guess whether an "@" reference is meant to be a file path rather than a
// literal like "@username". Backslash is only a path separator on Windows; on
// other platforms it is a valid filename character.
func looksLikeFilePath(filename string, goos string) bool {
	if strings.Contains(filename, ".") || strings.Contains(filename, "/") {
		return true
	}
	return goos == "windows" && strings.Contains(filename, "\\")
}

// IsUTF8TextFile reports whether content has a supported text MIME type and valid UTF-8.
func IsUTF8TextFile(content []byte) bool {
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
