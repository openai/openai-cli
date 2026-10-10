package custom

import (
	"errors"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// fileInputSource follows declared request fields through file expansion.
// It never retains argument values or infers an option from a file path.
type fileInputSource struct {
	option string
	fields map[string]*fileInputSource
}

func (s fileInputSource) child(field string) fileInputSource {
	if next := s.fields[field]; next != nil {
		result := *next
		if result.option == "" {
			result.option = s.option
		}
		return result
	}
	return fileInputSource{option: s.option}
}

func requestFileSource(command *cli.Command, area string) fileInputSource {
	var source fileInputSource
	for _, flag := range command.Flags {
		path, ok := requestFileFlagPath(flag, area)
		if !ok || len(flag.Names()) == 0 {
			continue
		}
		node := &source
		for _, field := range path {
			if node.fields == nil {
				node.fields = make(map[string]*fileInputSource)
			}
			if node.fields[field] == nil {
				node.fields[field] = &fileInputSource{}
			}
			node = node.fields[field]
		}
		prefix := "--"
		if len(flag.Names()[0]) == 1 {
			prefix = "-"
		}
		node.option = prefix + flag.Names()[0]
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

func embedRequestFiles(command *cli.Command, value any, area string, style FileEmbedStyle, stdin *onceStdinReader) (any, error) {
	return embedFilesForOS(value, style, stdin, runtime.GOOS, requestFileSource(command, area))
}

// inputFileError retains the actual failure and only a declared option name.
// Its cause remains available through errors.Is, errors.As, and joined errors.
type inputFileError struct {
	option, operation string
	err               error
}

func (e *inputFileError) Error() string { return e.err.Error() }
func (e *inputFileError) Unwrap() error { return e.err }

func (s fileInputSource) failure(operation string, err error) error {
	if err == nil || err == io.EOF || s.option == "" {
		return err
	}
	return &inputFileError{option: s.option, operation: operation, err: err}
}

func (s fileInputSource) readFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	operation := "read"
	var failure *os.PathError
	if errors.As(err, &failure) && failure.Op == "open" {
		operation = "open"
	}
	return data, s.failure(operation, err)
}

func (s fileInputSource) openUpload(path string) (fileUpload, error) {
	upload, err := openFileUpload(path)
	if err != nil {
		return upload, s.failure("open", err)
	}
	if s.option != "" {
		// Preserve fileUpload's filename, content type, and exact length metadata.
		upload.Reader = &inputFileReader{Reader: upload.Reader, source: s}
	}
	return upload, nil
}

type inputFileReader struct {
	io.Reader
	source fileInputSource
}

func (r *inputFileReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	return n, r.source.failure("read", err)
}

func (r *inputFileReader) Close() error {
	if closer, ok := r.Reader.(io.Closer); ok {
		return r.source.failure("close", closer.Close())
	}
	return nil
}

func inputFileErrorMessage(command *cli.Command, failure error) string {
	var messages []string
	var contextual *commandError
	if errors.As(failure, &contextual) && contextual.command != nil {
		command = contextual.command
	}
	add := func(message string) {
		if !slices.Contains(messages, message) {
			messages = append(messages, message)
		}
	}
	var visit func(error)
	visit = func(err error) {
		if err == nil {
			return
		}
		// Skills preparation supplies safe primary guidance. Preserve it while
		// the outer traversal continues through any sibling cleanup failures.
		if skill, ok := err.(*skillUploadError); ok {
			add(skill.Error())
			return
		}
		if input, ok := err.(*inputFileError); ok {
			add("Could not " + input.operation + " the file for " + input.option + ". Check the path and permissions.")
			return
		}
		var input *inputFileError
		if !errors.As(err, &input) {
			// A cleanup failure can accompany a primary failure from an unknown
			// field or stdin. Keep that branch's existing safe recovery guidance.
			add(localErrorMessage(command, err))
			return
		}
		switch wrapped := err.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range wrapped.Unwrap() {
				visit(child)
			}
		case interface{ Unwrap() error }:
			visit(wrapped.Unwrap())
		}
	}
	visit(failure)
	return strings.Join(messages, "\n")
}
