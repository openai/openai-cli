package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"runtime"
	"syscall"
	"testing"
)

func TestWriteAutomaticBinaryResponseRemovesIncompleteOwnedDestination(t *testing.T) {
	for _, failure := range []struct {
		name string
		err  error
	}{
		{name: "cancellation", err: context.Canceled},
		{name: "truncated response", err: io.ErrUnexpectedEOF},
		{name: "destination filesystem exhausted", err: syscall.ENOSPC},
	} {
		t.Run(failure.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			content := append([]byte{0xff}, bytes.Repeat([]byte("a"), 8192)...)
			response := &http.Response{
				Body: io.NopCloser(&errorAfterDownloadReader{
					Reader: bytes.NewReader(content),
					err:    failure.err,
				}),
				Header: http.Header{"Content-Disposition": []string{`attachment; filename="download.bin"`}},
			}

			if _, err := writeAutomaticBinaryResponse(response, io.Discard); !errors.Is(err, failure.err) {
				t.Errorf("writeAutomaticBinaryResponse(%s) error = %v, want %v", failure.name, err, failure.err)
			}
			if _, err := os.Lstat("download.bin"); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("os.Lstat(download.bin after %s) error = %v, want %v", failure.name, err, os.ErrNotExist)
			}
		})
	}
}

func TestWriteAutomaticBinaryResponsePreservesReplacementOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows prevents renaming an open destination handle")
	}
	t.Chdir(t.TempDir())
	body := &inspectingDownloadReader{
		Reader: &errorAfterDownloadReader{
			Reader: bytes.NewReader(append([]byte{0xff}, bytes.Repeat([]byte("a"), 8192)...)),
			err:    context.Canceled,
		},
		inspect: func() error {
			if err := os.Rename("download.bin", "original.bin"); err != nil {
				return err
			}
			return os.WriteFile("download.bin", []byte("unrelated replacement"), 0o600)
		},
	}
	response := &http.Response{
		Body:   io.NopCloser(body),
		Header: http.Header{"Content-Disposition": []string{`attachment; filename="download.bin"`}},
	}

	if _, err := writeAutomaticBinaryResponse(response, io.Discard); !errors.Is(err, context.Canceled) {
		t.Errorf("writeAutomaticBinaryResponse(replaced canceled destination) error = %v, want %v", err, context.Canceled)
	}
	content, err := os.ReadFile("download.bin")
	if err != nil || string(content) != "unrelated replacement" {
		t.Errorf("os.ReadFile(download.bin) = %q, %v, want %q, nil", content, err, "unrelated replacement")
	}
}

func TestWriteAutomaticBinaryResponseRejectsMissingCompletedDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows prevents renaming an open destination handle")
	}
	t.Chdir(t.TempDir())
	body := &inspectingDownloadReader{
		Reader: bytes.NewReader(append([]byte{0xff}, bytes.Repeat([]byte("a"), 8192)...)),
		inspect: func() error {
			return os.Rename("download.bin", "original.bin")
		},
	}
	response := &http.Response{
		Body:   io.NopCloser(body),
		Header: http.Header{"Content-Disposition": []string{`attachment; filename="download.bin"`}},
	}

	if _, err := writeAutomaticBinaryResponse(response, io.Discard); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("writeAutomaticBinaryResponse(missing completed destination) error = %v, want %v", err, os.ErrNotExist)
	}
	if _, err := os.Stat("original.bin"); err != nil {
		t.Errorf("os.Stat(original.bin) = %v, want original completed inode to remain", err)
	}
}
