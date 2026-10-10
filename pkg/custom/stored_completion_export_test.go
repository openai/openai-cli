package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStoredCompletionExportRecordPreservesJSON(t *testing.T) {
	for _, raw := range []string{
		` { "id" : "chatcmpl_demo", "unknown" : 9007199254740993, "number": 1.2300e+6, "null":null } `,
		`{"id":"chatcmpl_demo","text":"space \\ \" \u001b\n\t \u2028 雪","same":1,"same":2}`,
		"{\n\"id\": \"chatcmpl_demo\",\n\"array\": [ true, false, null, [1,2], {\"x\": \"\\\\\"} ]\n}",
		`{"id":"chatcmpl_demo","text":"` + strings.Repeat("x", 128*1024) + `"}`,
	} {
		var want, got bytes.Buffer
		if err := json.Compact(&want, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		want.WriteByte('\n')
		if complete, err := writeStoredCompletionRecord(t.Context(), &got, raw); err != nil || !complete {
			t.Fatal(err)
		}
		if got.String() != want.String() {
			t.Fatalf("JSONL bytes differ (got %d; want %d)", got.Len(), want.Len())
		}
	}
}

func TestStoredCompletionExportErrorRetainsWholeCause(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		failure := &storedCompletionExportError{message: "Export incomplete. Complete records processed: 1."}
		joined := errors.Join(failure, cause)
		message := storedCompletionExportMessage(joined, failure)
		want := "Request canceled.\n"
		if cause == context.DeadlineExceeded {
			want = "The request timed out.\n"
		}
		if !strings.HasPrefix(message, want) || !strings.Contains(message, "Complete records processed: 1.") {
			t.Fatalf("cause or outcome lost: %q", message)
		}
	}
}

type storedExportWriterFunc func([]byte) (int, error)

func (f storedExportWriterFunc) Write(data []byte) (int, error) { return f(data) }

func TestStoredCompletionExportRecordWriteFailuresAndCancellation(t *testing.T) {
	raw := `{"id":"chatcmpl_demo","text":"` + strings.Repeat("x", 128*1024) + `"}`
	for _, failure := range []error{nil, io.ErrClosedPipe} {
		_, err := writeStoredCompletionRecord(t.Context(), storedExportWriterFunc(func(data []byte) (int, error) {
			return len(data) - 1, failure
		}), raw)
		want := failure
		if want == nil {
			want = io.ErrShortWrite
		}
		if !errors.Is(err, want) {
			t.Fatalf("got %v; want %v", err, want)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writes := 0
	_, err := writeStoredCompletionRecord(ctx, storedExportWriterFunc(func(data []byte) (int, error) {
		writes++
		cancel()
		return len(data), nil
	}), raw)
	if !errors.Is(err, context.Canceled) || writes != 1 {
		t.Fatalf("cancellation: writes=%d error=%v", writes, err)
	}
	complete, err := writeStoredCompletionRecord(t.Context(), storedExportWriterFunc(func(data []byte) (int, error) {
		return len(data), io.ErrClosedPipe
	}), `{"id":"chatcmpl_demo"}`)
	if !complete || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("full write with error must count its complete line: complete=%v, err=%v", complete, err)
	}
}

func assertStoredExportStageCleanup(t *testing.T, directory string) {
	t.Helper()
	stages, err := filepath.Glob(filepath.Join(directory, ".openai-download-*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("staging cleanup: stages=%v err=%v", stages, err)
	}
}

func TestStoredCompletionExportSaveFailureKeepsDestinationAbsent(t *testing.T) {
	for _, failure := range []error{io.ErrShortWrite, context.Canceled} {
		directory := t.TempDir()
		path := filepath.Join(directory, "export.jsonl")
		err := saveStoredCompletionExport(t.Context(), path, func(out io.Writer) error {
			if _, err := io.WriteString(out, "partial"); err != nil {
				t.Fatal(err)
			}
			return failure
		})
		if !errors.Is(err, failure) {
			t.Fatalf("error cause lost: %v", err)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("incomplete destination exists: %v", err)
		}
		assertStoredExportStageCleanup(t, directory)
	}
}

func TestStoredCompletionExportSaveConcurrentDestination(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "export.jsonl")
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, payload := range []string{"first\n", "second\n"} {
		workers.Go(func() {
			results <- saveStoredCompletionExport(t.Context(), path, func(out io.Writer) error {
				_, err := io.WriteString(out, payload)
				ready <- struct{}{}
				<-release
				return err
			})
		})
	}
	<-ready
	<-ready
	close(release)
	workers.Wait()
	one, two := <-results, <-results
	if (one == nil) == (two == nil) {
		t.Fatalf("exactly one publisher must succeed: %v / %v", one, two)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "first\n" && string(data) != "second\n" {
		t.Fatalf("published bytes: %q, %v", data, err)
	}
	assertStoredExportStageCleanup(t, directory)
}

func TestStoredCompletionExportSaveParentReplacement(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "destination")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "export.jsonl")
	err := saveStoredCompletionExport(t.Context(), path, func(out io.Writer) error {
		if _, err := io.WriteString(out, "owned export\n"); err != nil {
			return err
		}
		if err := os.Rename(directory, directory+"-moved"); err != nil {
			return err
		}
		if err := os.Mkdir(directory, 0700); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("concurrent replacement"), 0600)
	})
	if !errors.Is(err, errDownloadDestinationChanged) {
		t.Fatalf("replacement not detected: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "concurrent replacement" {
		t.Fatalf("replacement changed: %q, %v", data, err)
	}
	assertStoredExportStageCleanup(t, directory+"-moved")
}
