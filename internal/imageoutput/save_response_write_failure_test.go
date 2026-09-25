//go:build darwin || linux

package imageoutput

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
)

func TestSaveResponseWriteFailure(t *testing.T) {
	// Isolate the process-wide file-size limit and signal disposition so this
	// test cannot affect other test packages or parallel tests.
	if os.Getenv("OPENAI_CLI_TEST_IMAGE_FILE_LIMIT") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestSaveResponseWriteFailure$")
		command.Env = append(os.Environ(), "OPENAI_CLI_TEST_IMAGE_FILE_LIMIT=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("write-failure subprocess: %v\n%s", err, output)
		}
		return
	}
	directory := t.TempDir()
	fixtures := imageFixtures(t)
	large := append(append([]byte(nil), fixtures[0]...), bytes.Repeat([]byte{1}, 64*1024)...)
	raw := imageResponse(t, fixtures[0], large, fixtures[1])
	var original syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
		t.Fatal(err)
	}
	limited := original
	limited.Cur = 8192
	signal.Ignore(syscall.SIGXFSZ)
	defer signal.Reset(syscall.SIGXFSZ)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Error(err)
		}
	}()
	paths, err := SaveResponse(t.Context(), raw, directory, "robot")
	if !errors.Is(err, syscall.EFBIG) || len(paths) != 2 || !strings.Contains(err.Error(), "saved 2 of 3 images") {
		t.Fatalf("partial write failure = %v, %v", paths, err)
	}
	for i, path := range paths {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, fixtures[i]) {
			t.Fatalf("completed image %d was changed: %v", i, err)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 2 {
		t.Fatalf("incomplete image remains after a real write failure: %v, %v", entries, err)
	}
}
