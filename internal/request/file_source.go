package request

import (
	"errors"
	"io"
	"os"
	"runtime"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// FileInputSource follows declared request fields through file expansion.
// It never retains argument values or infers an option from a file path.
type FileInputSource struct {
	Option string
	fields map[string]*FileInputSource
}

func (s FileInputSource) child(field string) FileInputSource {
	if next := s.fields[field]; next != nil {
		result := *next
		if result.Option == "" {
			result.Option = s.Option
		}
		return result
	}
	return FileInputSource{Option: s.Option}
}

func requestFileSource(command *cli.Command, area string) FileInputSource {
	var source FileInputSource
	for _, flag := range command.Flags {
		path, ok := requestFileFlagPath(flag, area)
		if !ok || len(flag.Names()) == 0 {
			continue
		}
		node := &source
		for _, field := range path {
			if node.fields == nil {
				node.fields = make(map[string]*FileInputSource)
			}
			if node.fields[field] == nil {
				node.fields[field] = &FileInputSource{}
			}
			node = node.fields[field]
		}
		prefix := "--"
		if len(flag.Names()[0]) == 1 {
			prefix = "-"
		}
		node.Option = prefix + flag.Names()[0]
	}
	return source
}

func requestFileFlagPath(flag cli.Flag, area string) ([]string, bool) {
	if inner, ok := flag.(requestflag.HasOuterFlag); ok {
		path, found := requestFileFlagPath(inner.GetOuterFlag(), area)
		return append(path, inner.GetInnerField()), found
	}
	field, ok := flag.(requestflag.InRequest)
	if !ok {
		return nil, false
	}
	var name string
	switch area {
	case "body":
		if field.IsBodyRoot() {
			return nil, true
		}
		name = field.GetBodyPath()
	case "header":
		name = field.GetHeaderPath()
	case "query":
		name = field.GetQueryPath()
	}
	return []string{name}, name != ""
}

// EmbedRequestFiles expands references with declared request-option provenance.
func EmbedRequestFiles(command *cli.Command, value any, area string, style FileEmbedStyle, stdin *OnceStdinReader) (any, error) {
	return embedFilesForOS(value, style, stdin, runtime.GOOS, requestFileSource(command, area))
}

// InputFileError retains the actual failure and only a declared option name.
// Its cause remains available through errors.Is, errors.As, and joined errors.
type InputFileError struct {
	Option, Operation string
	err               error
}

func (e *InputFileError) Error() string { return e.err.Error() }
func (e *InputFileError) Unwrap() error { return e.err }

// Failure preserves the original cause and its declared option, without wrapping a clean EOF.
func (s FileInputSource) Failure(operation string, err error) error {
	if err == nil || err == io.EOF || s.Option == "" {
		return err
	}
	return &InputFileError{Option: s.Option, Operation: operation, err: err}
}

// ReadFile reads a local source while retaining option metadata on failures.
func (s FileInputSource) ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	operation := "read"
	var failure *os.PathError
	if errors.As(err, &failure) && failure.Op == "open" {
		operation = "open"
	}
	return data, s.Failure(operation, err)
}

// OpenUpload opens an owned upload with option metadata on open, read, and close failures.
func (s FileInputSource) OpenUpload(path string) (FileUpload, error) {
	upload, err := OpenFileUpload(path)
	if err != nil {
		return upload, s.Failure("open", err)
	}
	if s.Option != "" {
		// Preserve FileUpload's filename, content type, and exact length metadata.
		upload.Reader = &InputFileReader{Reader: upload.Reader, Source: s}
	}
	return upload, nil
}

// InputFileReader annotates read and close failures with a declared option.
type InputFileReader struct {
	io.Reader
	Source FileInputSource
}

func (r *InputFileReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	return n, r.Source.Failure("read", err)
}

func (r *InputFileReader) Close() error {
	if closer, ok := r.Reader.(io.Closer); ok {
		return r.Source.Failure("close", closer.Close())
	}
	return nil
}
