//go:build !windows

package cmd

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWriteBinaryResponsePreservesExtendedAttributes(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "download.bin")
	if err := os.WriteFile(filename, []byte("original content"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) = %v, want nil", filename, err)
	}
	const attribute = "user.openai_cli_download"
	value := []byte("preserved metadata")
	if err := unix.Setxattr(filename, attribute, value, 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EPERM) {
			t.Skipf("destination filesystem does not support user extended attributes: %v", err)
		}
		t.Fatalf("unix.Setxattr(%q) = %v, want nil", filename, err)
	}

	response := &http.Response{Body: io.NopCloser(strings.NewReader("replacement"))}
	if _, err := writeBinaryResponse(response, io.Discard, filename); err != nil {
		t.Fatalf("writeBinaryResponse(%q) = %v, want nil", filename, err)
	}
	got := make([]byte, len(value))
	n, err := unix.Getxattr(filename, attribute, got)
	if err != nil {
		t.Fatalf("unix.Getxattr(%q) = %v, want preserved attribute", filename, err)
	}
	if string(got[:n]) != string(value) {
		t.Errorf("writeBinaryResponse(%q) xattr = %q, want %q", filename, got[:n], value)
	}
}
