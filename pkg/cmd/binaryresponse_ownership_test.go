package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteBinaryResponseRejectsReplacedExistingDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows prevents replacing an open destination handle")
	}

	for _, replacement := range []string{"regular file", "symlink"} {
		t.Run(replacement, func(t *testing.T) {
			directory := t.TempDir()
			outfile := filepath.Join(directory, "download.bin")
			original := filepath.Join(directory, "original.bin")
			victim := filepath.Join(directory, "victim.bin")
			if err := os.WriteFile(outfile, []byte("original content"), 0o600); err != nil {
				t.Fatalf("os.WriteFile(%q) = %v, want nil", outfile, err)
			}
			if err := os.WriteFile(victim, []byte("unrelated content"), 0o600); err != nil {
				t.Fatalf("os.WriteFile(%q) = %v, want nil", victim, err)
			}
			body := &inspectingDownloadReader{
				Reader: strings.NewReader("download content"),
				inspect: func() error {
					if err := os.Rename(outfile, original); err != nil {
						return err
					}
					if replacement == "symlink" {
						return os.Symlink(victim, outfile)
					}
					return os.WriteFile(outfile, []byte("concurrent replacement"), 0o600)
				},
			}
			response := &http.Response{Body: io.NopCloser(body)}

			if _, err := writeBinaryResponse(response, io.Discard, outfile); !errors.Is(err, os.ErrInvalid) {
				t.Errorf("writeBinaryResponse(replaced %s destination) error = %v, want changed-inode error %v", replacement, err, os.ErrInvalid)
			}
			for filename, want := range map[string]string{original: "original content", victim: "unrelated content"} {
				content, err := os.ReadFile(filename)
				if err != nil {
					t.Errorf("os.ReadFile(%q) = %v, want %q", filename, err, want)
					continue
				}
				if string(content) != want {
					t.Errorf("writeBinaryResponse(replaced %s destination) %q = %q, want %q", replacement, filename, content, want)
				}
			}
		})
	}
}

func TestWriteBinaryResponseRejectsReplacedPrivateStage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows prevents replacing an open staging handle")
	}

	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			directory := t.TempDir()
			outfile := filepath.Join(directory, "download.bin")
			if existing {
				if err := os.WriteFile(outfile, []byte("original content"), 0o600); err != nil {
					t.Fatalf("os.WriteFile(%q) = %v, want nil", outfile, err)
				}
			}
			var replaced string
			body := &inspectingDownloadReader{
				Reader: strings.NewReader("genuine download content"),
				inspect: func() error {
					matches, err := filepath.Glob(filepath.Join(directory, ".openai-cli-download-*"))
					if err != nil {
						return err
					}
					if len(matches) != 1 {
						return fmt.Errorf("private stage count = %d, want 1", len(matches))
					}
					replaced = matches[0]
					if err := os.Remove(replaced); err != nil {
						return err
					}
					return os.WriteFile(replaced, []byte("hostile stage replacement"), 0o600)
				},
			}
			response := &http.Response{Body: io.NopCloser(body)}

			if _, err := writeBinaryResponse(response, io.Discard, outfile); !errors.Is(err, os.ErrInvalid) {
				t.Errorf("writeBinaryResponse(replaced stage, existing=%t) error = %v, want changed-inode error %v", existing, err, os.ErrInvalid)
			}
			if existing {
				content, err := os.ReadFile(outfile)
				if err != nil {
					t.Fatalf("os.ReadFile(%q) = %v, want original content", outfile, err)
				}
				if string(content) != "original content" {
					t.Errorf("writeBinaryResponse(replaced stage) destination = %q, want %q", content, "original content")
				}
			} else if _, err := os.Stat(outfile); !os.IsNotExist(err) {
				t.Errorf("os.Stat(%q) error = %v, want no committed destination", outfile, err)
			}
			content, err := os.ReadFile(replaced)
			if err != nil {
				t.Fatalf("os.ReadFile(%q) = %v, want unrelated replacement preserved", replaced, err)
			}
			if string(content) != "hostile stage replacement" {
				t.Errorf("writeBinaryResponse(replaced stage) replacement = %q, want %q", content, "hostile stage replacement")
			}
		})
	}
}

func TestWriteAutomaticBinaryResponsePromotesDelayedBinaryStage(t *testing.T) {
	for _, test := range []struct {
		name            string
		collision       bool
		uniqueCollision bool
	}{
		{name: "preferred filename"},
		{name: "preferred filename collision", collision: true},
		{name: "unique candidate collision", collision: true, uniqueCollision: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			t.Chdir(directory)
			if test.collision {
				if err := os.WriteFile("download.bin", []byte("existing content"), 0o600); err != nil {
					t.Fatalf("os.WriteFile(download.bin) = %v, want nil", err)
				}
			}
			content := bytes.Repeat([]byte("a"), 16<<10)
			content[len(content)-1] = 0xff
			var staged os.FileInfo
			var reserved string
			body := &inspectAfterPrefixDownloadReader{
				Reader: bytes.NewReader(content),
				inspect: func() error {
					matches, err := filepath.Glob(".openai-cli-download-*")
					if err != nil {
						return err
					}
					if len(matches) != 1 {
						return fmt.Errorf("private stage count = %d, want 1", len(matches))
					}
					if test.uniqueCollision {
						suffix := strings.TrimPrefix(filepath.Base(matches[0]), ".openai-cli-download-")
						reserved = "download-" + suffix + ".bin"
						if err := os.WriteFile(reserved, []byte("reserved unique candidate"), 0o600); err != nil {
							return err
						}
					}
					staged, err = os.Stat(matches[0])
					return err
				},
			}
			response := &http.Response{
				Body:   io.NopCloser(body),
				Header: http.Header{"Content-Disposition": []string{`attachment; filename="download.bin"`}},
			}

			message, err := writeAutomaticBinaryResponse(response, io.Discard)
			if err != nil {
				t.Fatalf("writeAutomaticBinaryResponse(delayed binary, %s) = %v, want nil", test.name, err)
			}
			filename := strings.TrimPrefix(message, "Wrote output to: ")
			committed, err := os.Stat(filename)
			if err != nil {
				t.Fatalf("os.Stat(%q) = %v, want committed destination", filename, err)
			}
			if staged == nil || !os.SameFile(staged, committed) {
				t.Error("writeAutomaticBinaryResponse(delayed binary) copied its completed stage instead of promoting its original inode")
			}
			written, err := os.ReadFile(filename)
			if err != nil {
				t.Fatalf("os.ReadFile(%q) = %v, want original response", filename, err)
			}
			if !bytes.Equal(written, content) {
				t.Errorf("writeAutomaticBinaryResponse(delayed binary) content length = %d, want %d", len(written), len(content))
			}
			if test.collision {
				original, err := os.ReadFile("download.bin")
				if err != nil {
					t.Fatalf("os.ReadFile(download.bin) = %v, want original collision", err)
				}
				if string(original) != "existing content" {
					t.Errorf("writeAutomaticBinaryResponse(delayed binary collision) existing content = %q, want %q", original, "existing content")
				}
			}
			if test.uniqueCollision {
				original, err := os.ReadFile(reserved)
				if err != nil {
					t.Fatalf("os.ReadFile(%q) = %v, want original unique candidate", reserved, err)
				}
				if string(original) != "reserved unique candidate" {
					t.Errorf("writeAutomaticBinaryResponse(delayed binary collision) unique candidate = %q, want %q", original, "reserved unique candidate")
				}
			}
		})
	}
}
